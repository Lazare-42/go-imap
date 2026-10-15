package imapclient_test

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2/imapclient"
)

// TestList_skipsUndecodableMailboxName is an end-to-end regression test for a
// real production bug: a server can report a mailbox name containing literal
// control characters (not valid modified UTF-7), and that one bad name used
// to poison the whole LIST response, failing every other, perfectly valid
// mailbox in it and leaving the connection unusable. It must instead be
// skipped, with the rest of the response delivered normally and the
// connection left healthy for the next command.
func TestList_skipsUndecodableMailboxName(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- runFakeListServer(serverConn)
	}()

	client := imapclient.New(clientConn, nil)
	defer client.Close()

	if err := client.WaitGreeting(); err != nil {
		t.Fatalf("WaitGreeting() = %v", err)
	}

	cmd := client.List("", "%", nil)
	mailboxes, err := cmd.Collect()
	if err != nil {
		t.Fatalf("List().Collect() = %v", err)
	}

	var gotNames []string
	for _, mbox := range mailboxes {
		gotNames = append(gotNames, mbox.Mailbox)
	}
	wantNames := []string{"INBOX", "Archive"}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("List().Collect() returned %v, want %v", gotNames, wantNames)
	}
	for i, want := range wantNames {
		if gotNames[i] != want {
			t.Errorf("mailbox[%d] = %q, want %q", i, gotNames[i], want)
		}
	}

	skipped := cmd.SkippedNames()
	if len(skipped) != 1 || skipped[0] != "Some Folder\t\t" {
		t.Errorf("SkippedNames() = %q, want [%q]", skipped, "Some Folder\t\t")
	}

	// The whole point of the fix: the connection must still be usable after
	// the skip, not just the LIST command that encountered it.
	if err := client.Noop().Wait(); err != nil {
		t.Fatalf("Noop().Wait() after List with skipped name = %v", err)
	}
}

// runFakeListServer scripts a minimal IMAP server: greeting, then one LIST
// response containing a mailbox name with literal control characters among
// otherwise valid names, then replies OK to whatever commands follow. It
// deliberately writes the bad name as a raw quoted string (not modified
// UTF-7 encoded), matching what a noncompliant real-world server has been
// observed to send.
func runFakeListServer(conn net.Conn) error {
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)

	writeLine := func(s string) error {
		if _, err := w.WriteString(s + "\r\n"); err != nil {
			return err
		}
		return w.Flush()
	}

	if err := writeLine("* OK fake IMAP server ready"); err != nil {
		return err
	}

	listLine, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	tag := firstField(listLine)

	for _, line := range []string{
		`* LIST (\HasNoChildren) "/" "INBOX"`,
		"* LIST (\\HasNoChildren) \"/\" \"Some Folder\t\t\"",
		`* LIST (\HasNoChildren) "/" "Archive"`,
	} {
		if err := writeLine(line); err != nil {
			return err
		}
	}
	if err := writeLine(tag + " OK LIST completed"); err != nil {
		return err
	}

	// Reply OK to anything else the client sends (e.g. a CAPABILITY probe,
	// then NOOP), until it hangs up.
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil
		}
		if err := writeLine(firstField(line) + " OK done"); err != nil {
			return err
		}
	}
}

func firstField(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
