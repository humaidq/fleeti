/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"

	tpm2 "github.com/google/go-tpm/legacy/tpm2"
)

// buildTestQuote constructs a valid synthetic TPM quote signed by a software RSA
// key, mirroring what fleeti-tpm produces on a real TPM. It returns the AK public
// blob and the wire attestation.
func buildTestQuote(t *testing.T, nonce []byte, pcrs map[int][]byte) ([]byte, tpmAttestation) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	akPub := tpm2.Public{
		Type:       tpm2.AlgRSA,
		NameAlg:    tpm2.AlgSHA256,
		Attributes: tpm2.FlagSignerDefault,
		RSAParameters: &tpm2.RSAParams{
			Sign:       &tpm2.SigScheme{Alg: tpm2.AlgRSASSA, Hash: tpm2.AlgSHA256},
			KeyBits:    2048,
			ModulusRaw: key.N.Bytes(),
		},
	}

	akBlob, err := akPub.Encode()
	if err != nil {
		t.Fatalf("encode AK public: %v", err)
	}

	sel := tpm2.PCRSelection{Hash: tpm2.AlgSHA256, PCRs: pcrIndices(pcrs)}

	digest, err := computePCRDigest(sel, pcrs)
	if err != nil {
		t.Fatalf("compute digest: %v", err)
	}

	ad := tpm2.AttestationData{
		Magic:     0xff544347,
		Type:      tpm2.TagAttestQuote,
		ExtraData: nonce,
		AttestedQuoteInfo: &tpm2.QuoteInfo{
			PCRSelection: sel,
			PCRDigest:    digest,
		},
	}

	attestBytes, err := ad.Encode()
	if err != nil {
		t.Fatalf("encode attestation: %v", err)
	}

	h := sha256.Sum256(attestBytes)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	wire := tpmAttestation{
		Attest:    base64.StdEncoding.EncodeToString(attestBytes),
		Signature: base64.StdEncoding.EncodeToString(sig),
		PCRs:      map[string]string{},
	}
	for idx, value := range pcrs {
		wire.PCRs[strconv.Itoa(idx)] = hex.EncodeToString(value)
	}

	return akBlob, wire
}

func pcrIndices(pcrs map[int][]byte) []int {
	out := make([]int, 0, len(pcrs))
	for idx := range pcrs {
		out = append(out, idx)
	}

	return out
}

func TestVerifyQuoteValid(t *testing.T) {
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pcr7 := sha256.Sum256([]byte("secure-boot-state"))
	pcr11 := sha256.Sum256([]byte("uki-measurement"))
	pcrs := map[int][]byte{7: pcr7[:], 11: pcr11[:]}

	akBlob, wire := buildTestQuote(t, nonce, pcrs)

	values, err := verifyQuote(akBlob, wire, nonce)
	if err != nil {
		t.Fatalf("expected valid quote, got: %v", err)
	}

	if hex.EncodeToString(values[11]) != hex.EncodeToString(pcr11[:]) {
		t.Fatalf("PCR 11 mismatch")
	}

	if hex.EncodeToString(values[7]) != hex.EncodeToString(pcr7[:]) {
		t.Fatalf("PCR 7 mismatch")
	}
}

func TestVerifyQuoteNonceMismatch(t *testing.T) {
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pcr11 := sha256.Sum256([]byte("uki-measurement"))
	akBlob, wire := buildTestQuote(t, nonce, map[int][]byte{11: pcr11[:]})

	_, err := verifyQuote(akBlob, wire, []byte("a-different-nonce-of-same-length"))
	if !errors.Is(err, errQuoteVerification) {
		t.Fatalf("expected verification failure, got: %v", err)
	}
}

func TestVerifyQuoteTamperedPCR(t *testing.T) {
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pcr11 := sha256.Sum256([]byte("uki-measurement"))
	akBlob, wire := buildTestQuote(t, nonce, map[int][]byte{11: pcr11[:]})

	// Tamper with the reported PCR value; the signed digest no longer matches.
	tampered := sha256.Sum256([]byte("malicious-uki"))
	wire.PCRs["11"] = hex.EncodeToString(tampered[:])

	_, err := verifyQuote(akBlob, wire, nonce)
	if !errors.Is(err, errQuoteVerification) {
		t.Fatalf("expected verification failure for tampered PCR, got: %v", err)
	}
}

func TestVerifyQuoteBadSignature(t *testing.T) {
	nonce := []byte("0123456789abcdef0123456789abcdef")
	pcr11 := sha256.Sum256([]byte("uki-measurement"))
	akBlob, wire := buildTestQuote(t, nonce, map[int][]byte{11: pcr11[:]})

	// Corrupt the signature.
	sig, _ := base64.StdEncoding.DecodeString(wire.Signature)
	sig[0] ^= 0xff
	wire.Signature = base64.StdEncoding.EncodeToString(sig)

	_, err := verifyQuote(akBlob, wire, nonce)
	if !errors.Is(err, errQuoteVerification) {
		t.Fatalf("expected signature verification failure, got: %v", err)
	}
}
