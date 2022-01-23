package system

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

var (
	cr  = []byte(" \r")
	crr = []byte("\r\n")
)

// The maximum size of the buffer used to send output over the console to
// clients. Once this length is reached, the line will be truncated and sent
// as is.
var maxBufferSize = 64 * 1024

// FirstNotEmpty returns the first string passed in that is not an empty value.
func FirstNotEmpty(v ...string) string {
	for _, val := range v {
		if val != "" {
			return val
		}
	}
	return ""
}

func MustInt(v string) int {
	i, err := strconv.Atoi(v)
	if err != nil {
		panic(errors.Wrap(err, "system/utils: could not parse int"))
	}
	return i
}

// ScanReader reads up to 64KB of line from the reader and emits that value
// over the websocket. If a line exceeds that size, it is truncated and only that
// amount is sent over.
func ScanReader(r io.Reader, callback func(line []byte)) error {
	br := bufio.NewReader(r)
	// Avoid constantly re-allocating memory when we're flooding lines through this
	// function by using the same buffer for the duration of the call and just truncating
	// the value back to 0 every loop.
	var buf bytes.Buffer
	for {
		buf.Reset()
		var err error
		var line []byte
		var isPrefix bool

		for {
			// Read the line and write it to the buffer.
			line, isPrefix, err = br.ReadLine()

			// Certain games like Minecraft output absolutely random carriage returns in the output seemingly
			// in line with that it thinks is the terminal size. Those returns break a lot of output handling,
			// so we'll just replace them with proper new-lines and then split it later and send each line as
			// its own event in the response.
			line = bytes.Replace(line, cr, crr, -1)
			ns := buf.Len() + len(line)

			// If the length of the line value and the current value in the buffer will
			// exceed the maximum buffer size, chop it down to hit the maximum size and
			// then send that data over the socket before ending this loop.
			//
			// This ensures that we send as much data as possible, without allowing very
			// long lines to grow the buffer size excessively and potentially DOS the Wings
			// instance. If the line is not too long, just store the whole value into the
			// buffer. This is kind of a re-implementation of the bufio.Scanner.Scan() logic
			// without triggering an error when you exceed this buffer size.
			if ns > maxBufferSize {
				buf.Write(line[:len(line)-(ns-maxBufferSize)])
				break
			} else {
				buf.Write(line)
			}
			// Finish this loop and begin outputting the line if there is no prefix
			// (the line fit into the default buffer), or if we hit the end of the line.
			if !isPrefix || err == io.EOF {
				break
			}
			// If we encountered an error with something in ReadLine that was not an
			// EOF just abort the entire process here.
			if err != nil {
				return err
			}
		}

		// Send the full buffer length over to the event handler to be emitted in
		// the websocket. The front-end can handle the linebreaks in the middle of
		// the output, it simply expects that the end of the event emit is a newline.
		if buf.Len() > 0 {
			// You need to make a copy of the buffer here because the callback will encounter
			// a race condition since "buf.Bytes()" is going to be by-reference if passed directly.
			c := make([]byte, buf.Len())
			copy(c, buf.Bytes())
			callback(c)
		}

		// If the error we got previously that lead to the line being output is
		// an io.EOF we want to exit the entire looping process.
		if err == io.EOF {
			break
		}
	}
	return nil
}

// Runs a given work function every "d" duration until the provided context is canceled.
func Every(ctx context.Context, d time.Duration, work func(t time.Time)) {
	ticker := time.NewTicker(d)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case t := <-ticker.C:
				work(t)
			}
		}
	}()
}

func FormatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(1024), 0
	for n := b / 1024; n >= 1024; n /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

type AtomicBool struct {
	v  bool
	mu sync.RWMutex
}

func NewAtomicBool(v bool) *AtomicBool {
	return &AtomicBool{v: v}
}

func (ab *AtomicBool) Store(v bool) {
	ab.mu.Lock()
	ab.v = v
	ab.mu.Unlock()
}

// Stores the value "v" if the current value stored in the AtomicBool is the opposite
// boolean value. If successfully swapped, the response is "true", otherwise "false"
// is returned.
func (ab *AtomicBool) SwapIf(v bool) bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	if ab.v != v {
		ab.v = v
		return true
	}
	return false
}

func (ab *AtomicBool) Load() bool {
	ab.mu.RLock()
	defer ab.mu.RUnlock()
	return ab.v
}

func (ab *AtomicBool) UnmarshalJSON(b []byte) error {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return json.Unmarshal(b, &ab.v)
}

func (ab *AtomicBool) MarshalJSON() ([]byte, error) {
	return json.Marshal(ab.Load())
}

// AtomicString allows for reading/writing to a given struct field without having to worry
// about a potential race condition scenario. Under the hood it uses a simple sync.RWMutex
// to control access to the value.
type AtomicString struct {
	v  string
	mu sync.RWMutex
}

func NewAtomicString(v string) *AtomicString {
	return &AtomicString{v: v}
}

// Stores the string value passed atomically.
func (as *AtomicString) Store(v string) {
	as.mu.Lock()
	as.v = v
	as.mu.Unlock()
}

// Loads the string value and returns it.
func (as *AtomicString) Load() string {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return as.v
}

func (as *AtomicString) UnmarshalJSON(b []byte) error {
	as.mu.Lock()
	defer as.mu.Unlock()
	return json.Unmarshal(b, &as.v)
}

func (as *AtomicString) MarshalJSON() ([]byte, error) {
	return json.Marshal(as.Load())
}
