package sftp

import (
	"context"
	"io"

	"github.com/schollz/progressbar/v3"
)

// progressReader wraps an io.Reader to track progress with batched updates
type progressReader struct {
	reader           io.Reader
	bar              *progressbar.ProgressBar
	size             int64
	bytesSinceUpdate int64
}

// batchSize for progress updates (512KB)
const progressBatchSize = 512 * 1024

// Read implements io.Reader with batched progress updates.
func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.reader.Read(p)
	if n > 0 {
		pr.bytesSinceUpdate += int64(n)
		// Batch progress updates to reduce overhead
		if pr.bytesSinceUpdate >= progressBatchSize {
			pr.bar.Add64(pr.bytesSinceUpdate)
			pr.bytesSinceUpdate = 0
		}
	}
	return
}

// Size returns the total size.
func (pr *progressReader) Size() int64 {
	return pr.size
}

// Flush updates any pending progress.
func (pr *progressReader) Flush() {
	if pr.bytesSinceUpdate > 0 {
		pr.bar.Add64(pr.bytesSinceUpdate)
		pr.bytesSinceUpdate = 0
	}
}

// progressWriter wraps an io.Writer to update progress bar with batched updates
type progressWriter struct {
	writer           io.Writer
	bar              *progressbar.ProgressBar
	ctx              context.Context
	bytesSinceUpdate int64
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	// Check context
	select {
	case <-pw.ctx.Done():
		return 0, context.Canceled
	default:
	}

	n, err = pw.writer.Write(p)
	if n > 0 {
		pw.bytesSinceUpdate += int64(n)
		// Batch progress updates
		if pw.bytesSinceUpdate >= progressBatchSize {
			pw.bar.Add64(pw.bytesSinceUpdate)
			pw.bytesSinceUpdate = 0
		}
	}
	return
}

// Flush updates any pending progress.
func (pw *progressWriter) Flush() {
	if pw.bytesSinceUpdate > 0 {
		pw.bar.Add64(pw.bytesSinceUpdate)
		pw.bytesSinceUpdate = 0
	}
}

// progressWriterFrom wraps an io.Writer to implement io.ReaderFrom with progress tracking.
// Kept for backward compatibility with non-context version of cmdGet.
type progressWriterFrom struct {
	writer           io.Writer
	bar              *progressbar.ProgressBar
	ctx              context.Context
	bytesSinceUpdate int64
}

func newProgressWriterFrom(w io.Writer, bar *progressbar.ProgressBar) *progressWriterFrom {
	return &progressWriterFrom{
		writer: w,
		bar:    bar,
		ctx:    context.Background(),
	}
}

func (pwf *progressWriterFrom) ReadFrom(r io.Reader) (n int64, err error) {
	buf := make([]byte, 1024*1024)
	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			written := 0
			for written < nr {
				nw, ew := pwf.writer.Write(buf[written:nr])
				if nw > 0 {
					written += nw
					n += int64(nw)
					pwf.bytesSinceUpdate += int64(nw)
					// Batch progress updates
					if pwf.bytesSinceUpdate >= progressBatchSize {
						pwf.bar.Add64(pwf.bytesSinceUpdate)
						pwf.bytesSinceUpdate = 0
					}
				}
				if ew != nil {
					return n, ew
				}
				if nw == 0 {
					return n, io.ErrShortWrite
				}
			}
		}
		if er != nil {
			if er == io.EOF {
				break
			}
			return n, er
		}
	}
	// Flush remaining progress
	if pwf.bytesSinceUpdate > 0 {
		pwf.bar.Add64(pwf.bytesSinceUpdate)
	}
	return n, nil
}

// progressWriterTo wraps an io.Reader for upload progress tracking with batched updates.
// Kept for backward compatibility with non-context version of cmdPut.
type progressWriterTo struct {
	reader           io.Reader
	bar              *progressbar.ProgressBar
	size             int64
	bytesSinceUpdate int64
}

func newProgressWriterTo(r io.Reader, bar *progressbar.ProgressBar, size int64) *progressWriterTo {
	return &progressWriterTo{
		reader: r,
		bar:    bar,
		size:   size,
	}
}

func (pwt *progressWriterTo) Read(p []byte) (n int, err error) {
	n, err = pwt.reader.Read(p)
	if n > 0 {
		pwt.bytesSinceUpdate += int64(n)
		// Batch progress updates to reduce overhead
		if pwt.bytesSinceUpdate >= progressBatchSize {
			pwt.bar.Add64(pwt.bytesSinceUpdate)
			pwt.bytesSinceUpdate = 0
		}
	}
	return
}

func (pwt *progressWriterTo) Size() int64 {
	return pwt.size
}
