// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux

package torrent

import (
	"os"
	"syscall"
)

func stableFileStamp(path string, info os.FileInfo) (memoFileStamp, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return memoFileStamp{}, false
	}
	return memoFileStamp{
		Path:       path,
		Device:     stat.Dev,
		Inode:      stat.Ino,
		Size:       info.Size(),
		Mode:       uint32(info.Mode()),
		ModifiedNS: info.ModTime().UnixNano(),
		ChangedNS:  stat.Ctim.Sec*1e9 + stat.Ctim.Nsec,
	}, true
}
