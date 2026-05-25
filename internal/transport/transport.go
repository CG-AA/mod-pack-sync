// Package transport wraps magic-wormhole peer-to-peer file transfer.
package transport

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/psanford/wormhole-william/wormhole"
)

// ProgressFunc receives transferred/total byte counts.
type ProgressFunc func(sent, total int64)

func client(relay string) *wormhole.Client {
	c := &wormhole.Client{}
	if relay != "" {
		c.RendezvousURL = relay
	}
	return c
}

// SendFile sends the file at path and invokes codeFn with the generated code
// phrase as soon as it is available, then blocks until the transfer completes.
func SendFile(ctx context.Context, relay, path string, codeFn func(string), prog ProgressFunc) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}

	c := client(relay)
	var opts []wormhole.SendOption
	if prog != nil {
		opts = append(opts, wormhole.WithProgress(func(sent, total int64) { prog(sent, total) }))
	}
	code, status, err := c.SendFile(ctx, fi.Name(), f, opts...)
	if err != nil {
		return err
	}
	codeFn(code)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-status:
		if !res.OK {
			return fmt.Errorf("transfer failed: %w", res.Error)
		}
	}
	return nil
}

// Receive downloads the file identified by code, writing it to destPath.
func Receive(ctx context.Context, relay, code, destPath string, prog ProgressFunc) error {
	c := client(relay)
	msg, err := c.Receive(ctx, code)
	if err != nil {
		return err
	}
	if msg.Type != wormhole.TransferFile {
		return fmt.Errorf("expected a file transfer, got a different type")
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	total := msg.TransferBytes64
	var reader io.Reader = msg
	if prog != nil {
		reader = &progressReader{r: msg, total: total, cb: prog}
	}
	if _, err := io.Copy(out, reader); err != nil {
		return err
	}
	if prog != nil {
		prog(total, total)
	}
	return nil
}

type progressReader struct {
	r     io.Reader
	read  int64
	total int64
	cb    ProgressFunc
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	p.cb(p.read, p.total)
	return n, err
}
