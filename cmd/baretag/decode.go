package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mjovanovic/baretag/internal/payload"
)

// decodeMain turns scanned payloads back into readable JSON. It is the other
// half of the workflow: the technician scans the screen, pastes what the phone
// produced into this command on a machine that does have a keyboard.
func decodeMain(args []string) error {
	parts := args
	if len(parts) == 0 {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			parts = append(parts, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	}
	if len(parts) == 0 {
		return fmt.Errorf("no payload given; pass the scanned strings as arguments or on stdin")
	}

	raw, err := payload.Decode(parts)
	if err != nil {
		return err
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(raw), "", "  "); err != nil {
		// Not JSON after all: hand back whatever was scanned rather than
		// failing, so a mistyped paste is still visible.
		fmt.Println(raw)
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}
