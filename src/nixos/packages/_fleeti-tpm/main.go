/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */

// Command fleeti-tpm performs the device side of Fleeti's TPM remote attestation.
// It creates a deterministic, non-exportable attestation key (AK) in the TPM and
// produces signed quotes over the boot-measurement PCRs. The Python device agent
// shells out to it; the Fleeti server verifies its output.
//
// The AK is a restricted RSA signing primary derived deterministically from the
// owner hierarchy seed and a fixed template, so the same key is reproduced on
// every invocation without storing any private material on disk. Different TPMs
// (different devices) derive different keys, giving each device a stable identity.
//
// Usage:
//
//	fleeti-tpm init                      # print the AK public area (base64)
//	fleeti-tpm quote --nonce <hex> --pcrs 7,11
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	tpm2 "github.com/google/go-tpm/legacy/tpm2"
	"github.com/google/go-tpm/tpmutil"
)

// akTemplate is the fixed AK public template. FlagSignerDefault marks the key as
// a restricted, fixed-TPM, fixed-parent signing key, so it can only ever sign
// TPM-generated structures (such as quotes) and can never leave the TPM.
var akTemplate = tpm2.Public{
	Type:       tpm2.AlgRSA,
	NameAlg:    tpm2.AlgSHA256,
	Attributes: tpm2.FlagSignerDefault,
	RSAParameters: &tpm2.RSAParams{
		Sign:    &tpm2.SigScheme{Alg: tpm2.AlgRSASSA, Hash: tpm2.AlgSHA256},
		KeyBits: 2048,
	},
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: fleeti-tpm <init|quote> [options]")
	}

	switch os.Args[1] {
	case "init":
		runInit()
	case "quote":
		runQuote(os.Args[2:])
	default:
		fail("unknown subcommand %q (expected init or quote)", os.Args[1])
	}
}

func runInit() {
	rw, err := openTPM()
	if err != nil {
		fail("opening TPM: %v", err)
	}
	defer rw.Close()

	handle, akPublic, err := createAK(rw)
	if err != nil {
		fail("creating attestation key: %v", err)
	}
	defer flush(rw, handle)

	writeJSON(map[string]string{
		"ak_public": base64.StdEncoding.EncodeToString(akPublic),
	})
}

func runQuote(args []string) {
	var nonceHex, pcrList string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--nonce":
			i++
			if i < len(args) {
				nonceHex = args[i]
			}
		case "--pcrs":
			i++
			if i < len(args) {
				pcrList = args[i]
			}
		default:
			fail("unknown option %q", args[i])
		}
	}

	if strings.TrimSpace(nonceHex) == "" {
		fail("--nonce is required")
	}

	nonce, err := hex.DecodeString(strings.TrimSpace(nonceHex))
	if err != nil {
		fail("--nonce must be hex: %v", err)
	}

	pcrs, err := parsePCRList(pcrList)
	if err != nil {
		fail("%v", err)
	}

	rw, err := openTPM()
	if err != nil {
		fail("opening TPM: %v", err)
	}
	defer rw.Close()

	handle, akPublic, err := createAK(rw)
	if err != nil {
		fail("creating attestation key: %v", err)
	}
	defer flush(rw, handle)

	sel := tpm2.PCRSelection{Hash: tpm2.AlgSHA256, PCRs: pcrs}

	attest, sig, err := tpm2.Quote(rw, handle, "", "", nonce, sel, tpm2.AlgNull)
	if err != nil {
		fail("quoting PCRs: %v", err)
	}

	if sig.RSA == nil {
		fail("quote signature is not RSA")
	}

	values, err := readPCRValues(rw, pcrs)
	if err != nil {
		fail("reading PCR values: %v", err)
	}

	out := map[string]any{
		"attest":    base64.StdEncoding.EncodeToString(attest),
		"signature": base64.StdEncoding.EncodeToString(sig.RSA.Signature),
		"pcrs":      values,
		"ak_public": base64.StdEncoding.EncodeToString(akPublic),
	}

	writeJSON(out)
}

// createAK reproduces the deterministic attestation key under the owner hierarchy
// and returns its transient handle and marshaled public area (TPMT_PUBLIC).
func createAK(rw io.ReadWriter) (tpmutil.Handle, []byte, error) {
	handle, public, _, _, _, _, err := tpm2.CreatePrimaryEx(rw, tpm2.HandleOwner, tpm2.PCRSelection{}, "", "", akTemplate)
	if err != nil {
		return 0, nil, err
	}

	return handle, public, nil
}

// readPCRValues returns the selected PCRs as a map of decimal index -> hex sha256.
// ReadPCRs may cap the number of PCRs returned per call, so it loops until every
// requested PCR is read.
func readPCRValues(rw io.ReadWriter, pcrs []int) (map[string]string, error) {
	out := make(map[string]string, len(pcrs))

	remaining := append([]int(nil), pcrs...)
	for len(remaining) > 0 {
		got, err := tpm2.ReadPCRs(rw, tpm2.PCRSelection{Hash: tpm2.AlgSHA256, PCRs: remaining})
		if err != nil {
			return nil, err
		}

		if len(got) == 0 {
			return nil, fmt.Errorf("TPM returned no PCR values")
		}

		next := remaining[:0]
		for _, idx := range remaining {
			value, ok := got[idx]
			if !ok {
				next = append(next, idx)

				continue
			}

			out[strconv.Itoa(idx)] = hex.EncodeToString(value)
		}

		remaining = next
	}

	return out, nil
}

func parsePCRList(list string) ([]int, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return nil, fmt.Errorf("--pcrs is required (e.g. 7,11)")
	}

	seen := make(map[int]bool)
	var pcrs []int
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		idx, err := strconv.Atoi(part)
		if err != nil || idx < 0 || idx > 23 {
			return nil, fmt.Errorf("invalid PCR index %q", part)
		}

		if !seen[idx] {
			seen[idx] = true
			pcrs = append(pcrs, idx)
		}
	}

	if len(pcrs) == 0 {
		return nil, fmt.Errorf("--pcrs is required (e.g. 7,11)")
	}

	sort.Ints(pcrs)

	return pcrs, nil
}

// openTPM opens the TPM resource-manager device, honoring FLEETI_TPM_DEVICE for
// tests/overrides and otherwise falling back to /dev/tpmrm0 then /dev/tpm0.
func openTPM() (io.ReadWriteCloser, error) {
	if path := strings.TrimSpace(os.Getenv("FLEETI_TPM_DEVICE")); path != "" {
		return tpm2.OpenTPM(path)
	}

	return tpm2.OpenTPM()
}

func flush(rw io.ReadWriter, handle tpmutil.Handle) {
	_ = tpm2.FlushContext(rw, handle)
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(value); err != nil {
		fail("encoding output: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fleeti-tpm: "+format+"\n", args...)
	os.Exit(1)
}
