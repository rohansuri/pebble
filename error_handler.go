// Copyright 2020 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"sync"

	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/vfs"
)

// BackgroundErrorReason is an enum of possible operations whose failure may
// put the DB in read-only mode depending on the severity.
type BackgroundErrorReason uint8

const (
	// BgFlush is for errors during background flushes.
	BgFlush BackgroundErrorReason = iota
	// BgCompaction is for errors during background compactions.
	BgCompaction

	// TODO: Export these once we stop panicking for them.
	// BgWrite is for errors in writing to WAL.
	bgWrite
	// BgMemtable is for errors in writing to memtable.
	bgMemtable
	// BgManifestWrite is for errors in writing to MANIFEST.
	bgManifestWrite
)

// Severity of an error indicates whether recovery is possible and whether DB
// will be placed in read-only mode or not.
type Severity uint8

const (
	// SeverityNoError does not set the background error at all.
	SeverityNoError Severity = 0
	// SeveritySoftError does not place the DB in read-only mode and auto recovery
	// is possible for some of the errors.
	SeveritySoftError = 1
	// SeverityHardError places the DB in read-only mode and recovery might be
	// possible without needing to close then reopen the DB.
	SeverityHardError = 2
	// SeverityFatalError places the DB in read-only mode and recovery requires
	// closing then reopening the DB.
	SeverityFatalError = 3
	// SeverityUnrecoverableError places the DB in read-only mode and could mean
	// data loss, recovery from which is not possible.
	SeverityUnrecoverableError = 4
)

// BackgroundError captures the error occurred during any background operation
// along with its severity. An instance of this is passed in
// EventListener.BackgroundError.
type BackgroundError struct {
	err      error
	severity Severity
	reason   BackgroundErrorReason
}

// Reason returns the operation during which the error occurred.
func (b BackgroundError) Reason() BackgroundErrorReason {
	return b.reason
}

// Severity returns the severity of the error.
func (b BackgroundError) Severity() Severity {
	return b.severity
}

// Unwrap returns the error that occurred during the background operation.
func (b BackgroundError) Unwrap() error {
	return b.err
}

func (b BackgroundError) Error() string {
	if b.err != nil {
		return b.err.Error()
	}
	return ""
}

type errorHandler struct {
	// Set to db.mu.
	mu   *sync.Mutex
	opts *Options
	err  BackgroundError
}

func (h *errorHandler) init(opts *Options, mu *sync.Mutex) {
	h.opts = opts
	h.mu = mu
}

// Returns true if DB is placed in read-only mode. Requires db.mu is held.
func (h *errorHandler) isDBStopped() bool {
	return h.err.err != nil && h.err.severity >= SeverityHardError
}

// Requires db.mu is held.
func (h *errorHandler) isBGWorkStopped() bool {
	return h.err.err != nil && h.err.severity >= SeverityHardError
}

func (h *errorHandler) getSeverity(err error, op BackgroundErrorReason) Severity {
	if op == bgMemtable {
		return SeverityFatalError
	}
	if vfs.IsNoSpaceError(err) {
		switch op {
		case BgCompaction:
			return SeveritySoftError
		case BgFlush, bgWrite, bgManifestWrite:
			return SeverityHardError
		default:
			panic("unreachable")
		}
	}
	if errors.Is(err, base.ErrCorruption) {
		return SeverityUnrecoverableError
	}
	// Default severity for all other errors.
	return SeverityFatalError
}

// Requires db.mu is held.
func (h *errorHandler) setBGError(err error, op BackgroundErrorReason) {
	if err == nil {
		return
	}
	sev := h.getSeverity(err, op)
	bgErr := BackgroundError{
		err:      err,
		severity: sev,
		reason:   op,
	}

	h.mu.Unlock()
	h.opts.EventListener.BackgroundError(bgErr)
	h.mu.Lock()

	if bgErr.severity > h.err.severity {
		h.err = bgErr
	}
}

// Requires db.mu is held.
func (h *errorHandler) getBGError() error {
	err := h.err
	return &err
}