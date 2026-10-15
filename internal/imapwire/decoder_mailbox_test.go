package imapwire

import (
	"bufio"
	"strings"
	"testing"
)

// TestExpectMailboxLenientSkipsControlCharacterNames locks in the fix for a
// real production bug: a server (observed on Exchange Online) can report a
// mailbox name containing literal control characters — e.g. a folder a user
// renamed in Outlook to end in two tab bytes — which modified UTF-7 has no
// valid encoding for. ExpectMailbox's dec.returnErr sets a sticky decoder
// error on that one bad name, which then fails every subsequent read on the
// same connection, taking down an entire LIST response (and every other,
// perfectly valid mailbox in it) over one malformed folder.
// ExpectMailboxLenient must instead report skip=true and leave the decoder
// healthy, so a caller can skip just that one mailbox and keep reading.
func TestExpectMailboxLenientSkipsControlCharacterNames(t *testing.T) {
	tests := []struct {
		name       string
		wire       string // the astring exactly as it would appear on the wire
		wantOK     bool
		wantSkip   bool
		wantResult string // *ptr after the call, when wantOK or wantSkip
	}{
		{
			name:       "plain ASCII name decodes normally",
			wire:       `"INBOX.Work"`,
			wantOK:     true,
			wantSkip:   false,
			wantResult: "INBOX.Work",
		},
		{
			name:       "INBOX is case-insensitive and never UTF-7 decoded",
			wire:       `"inbox"`,
			wantOK:     true,
			wantSkip:   false,
			wantResult: "INBOX",
		},
		{
			name:       "valid modified UTF-7 name decodes normally",
			wire:       `"&AOkA6Q-"`, // single shift run, two U+00E9 (é) code units
			wantOK:     true,
			wantSkip:   false,
			wantResult: "éé",
		},
		{
			// Matches the shape of the production repro: a folder a user
			// renamed client-side to end in trailing tab bytes, reported by
			// the server as a quoted string.
			name:       "name with literal control characters is skipped, not fatal",
			wire:       "\"Some Folder\t\t\"",
			wantOK:     false,
			wantSkip:   true,
			wantResult: "Some Folder\t\t", // raw name, diagnostic only
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dec := NewDecoder(bufio.NewReader(strings.NewReader(test.wire+"\r\n")), ConnSideClient)

			var got string
			ok, skip := dec.ExpectMailboxLenient(&got)
			if ok != test.wantOK || skip != test.wantSkip {
				t.Fatalf("ExpectMailboxLenient() = (ok=%v, skip=%v), want (ok=%v, skip=%v)",
					ok, skip, test.wantOK, test.wantSkip)
			}
			if got != test.wantResult {
				t.Errorf("*ptr = %q, want %q", got, test.wantResult)
			}
			// The whole point of the fix: no sticky decoder error, even on
			// the skip path. A caller must be able to keep reading this same
			// decoder for the next response.
			if err := dec.Err(); err != nil {
				t.Errorf("decoder left with sticky error after skip: %v", err)
			}
		})
	}
}
