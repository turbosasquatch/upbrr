// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux && !darwin

package torrent

import "os"

// Unknown stat contracts always take the existing full verification path.
func stableFileStamp(string, os.FileInfo) (memoFileStamp, bool) { return memoFileStamp{}, false }
