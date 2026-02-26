/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"net/http"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"
)

type BreadcrumbItem struct {
	Name      string
	URL       string
	IsCurrent bool
}

func setPage(data template.Data, title string) {
	data["PageTitle"] = title
}

func setBreadcrumbs(data template.Data, items []BreadcrumbItem) {
	data["Breadcrumbs"] = items
}

func redirectWithMessage(c flamego.Context, s session.Session, path string, messageType FlashType, message string) {
	SetFlash(s, messageType, message)
	c.Redirect(path, http.StatusSeeOther)
}
