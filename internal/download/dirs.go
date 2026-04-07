package download

import "os"

// downloadDirMode is the directory mode used for per-show download dirs.
// Must include the group-write bit so that an *arr stack running as a
// different user in the same group (e.g. UNRAID with PUID=99 PGID=100
// against hotio's UMASK=002) can move and delete files after import.
// See issue #12.
const downloadDirMode = 0o775

// EnsureDownloadDir creates path with downloadDirMode (subject to umask).
// Centralised so tests can assert the mode and so we never accidentally
// regress to a non-group-writable default.
func EnsureDownloadDir(path string) error {
	return os.MkdirAll(path, downloadDirMode)
}
