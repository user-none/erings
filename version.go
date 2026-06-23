// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package erings

import "strings"

const (
	Name = "erings"

	// defaultVersion is reported when no build-time version is available.
	defaultVersion = "0.0.0"
)

// Version is the version reported to the UI. It may be overridden at link
// time with -ldflags "-X github.com/user-none/erings.Version=...". When it is
// not overridden, init resolves it from the git archive substitution.
var Version = defaultVersion

// archiveVersion is substituted by git archive export-subst (see
// .gitattributes) when building from a release source archive, such as the
// tarball GitHub generates for a tagged release. In a working tree it remains
// the literal placeholder.
var archiveVersion = "$Format:%(describe:tags)$"

func init() {
	// A link-time override (CI and make builds) takes precedence.
	if Version != defaultVersion {
		return
	}

	// A release source archive carries the tag via export-subst.
	if !strings.HasPrefix(archiveVersion, "$Format:") && archiveVersion != "" {
		Version = archiveVersion
	}
}
