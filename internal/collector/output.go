package collector

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// Bounds on the poll output read from a server. Process records are
// capped by count instead of bytes so that a host running very many
// processes still reports every other metric, with a partial process
// list; all other output, section markers included, is capped at
// maxOutput. Memory per poll is thus bounded by
// maxOutput + maxProcRecords*maxProcRecord whatever the host prints.
const (
	maxOutput      = 4 << 20
	maxProcRecords = 32768
	// maxProcRecord exceeds the longest valid record: seven numbers
	// and a comm of at most 64 bytes.
	maxProcRecord = 256
)

var errOutputTooLarge = fmt.Errorf("output larger than %d bytes", maxOutput)

// pollOutput is the bounded output of one poll.
type pollOutput struct {
	text string
	// procsTruncated reports that the procs section had more than
	// maxProcRecords lines and only the first ones were kept.
	procsTruncated bool
}

// readOutput reads the output of pollScript(nonce) from r. Lines of the
// procs section are kept while fewer than maxProcRecords were kept and
// if they are at most maxProcRecord bytes long; the rest of the section
// is read and discarded. Any other output beyond maxOutput bytes fails
// with errOutputTooLarge.
func readOutput(r io.Reader, nonce string) (pollOutput, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	marker := []byte(markerPrefix + nonce + ":")
	var buf bytes.Buffer
	var out pollOutput
	other, records := 0, 0
	inProcs, lineStart := false, true
	for {
		chunk, err := br.ReadSlice('\n')
		complete := err == nil
		switch {
		case lineStart && bytes.HasPrefix(chunk, marker):
			inProcs = string(bytes.TrimSpace(chunk[len(marker):])) == secProcs
			fallthrough
		case !inProcs:
			if other += len(chunk); other > maxOutput {
				return pollOutput{}, errOutputTooLarge
			}
			buf.Write(chunk)
		case !lineStart || !complete || len(chunk) > maxProcRecord:
			// (part of) an overlong, hence invalid, record
		case len(bytes.TrimSpace(chunk)) == 0:
			// blank line before the next marker
		case records >= maxProcRecords:
			out.procsTruncated = true
		default:
			records++
			buf.Write(chunk)
		}
		lineStart = complete
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			out.text = buf.String()
			return out, nil
		default:
			return pollOutput{}, err
		}
	}
}
