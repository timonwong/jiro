package cmd

import (
	"context"
	"io"
	"os"
)

type contextOpenResult struct {
	file *os.File
	err  error
}

// openFileWithContext abandons an open that blocks past ctx, such as a FIFO
// waiting for its writer.
func openFileWithContext(ctx context.Context, path string) (*os.File, error) {
	result := make(chan contextOpenResult, 1)
	go func() {
		file, err := os.Open(path)
		result <- contextOpenResult{file: file, err: err}
	}()
	select {
	case completed := <-result:
		return completed.file, completed.err
	case <-ctx.Done():
		go func() {
			completed := <-result
			if completed.file != nil {
				_ = completed.file.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

type contextReadResult struct {
	data []byte
	err  error
}

// readAllWithContext abandons a read that blocks past ctx, closing closer so a
// blocked reader can observe the end of its input.
func readAllWithContext(ctx context.Context, reader io.Reader, closer io.Closer) ([]byte, error) {
	result := make(chan contextReadResult, 1)
	go func() {
		data, err := io.ReadAll(reader)
		result <- contextReadResult{data: data, err: err}
	}()
	select {
	case completed := <-result:
		return completed.data, completed.err
	case <-ctx.Done():
		if closer != nil {
			_ = closer.Close()
		}
		return nil, ctx.Err()
	}
}

func readFileWithContext(ctx context.Context, path string) ([]byte, error) {
	file, err := openFileWithContext(ctx, path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readAllWithContext(ctx, file, file)
}
