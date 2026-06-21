// Command gidm-host is the Chrome native-messaging host. The browser launches it
// and exchanges length-prefixed JSON over stdio; the host validates the calling
// extension's origin and forwards requests to the daemon. Forwarding lands in
// milestone M4; for now it frames messages and replies "unimplemented".
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const maxMessageSize = 64 << 20 // Chrome's extension->host limit

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, "gidm-host:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	for {
		if _, err := readMessage(in); err != nil {
			return err
		}
		reply, _ := json.Marshal(map[string]string{
			"status": "unimplemented",
			"detail": "gidm-host forwarding arrives in milestone M4",
		})
		if err := writeMessage(out, reply); err != nil {
			return err
		}
	}
}

func readMessage(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.NativeEndian.Uint32(lenBuf[:])
	if n > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMessage(w io.Writer, msg []byte) error {
	var lenBuf [4]byte
	binary.NativeEndian.PutUint32(lenBuf[:], uint32(len(msg)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}
