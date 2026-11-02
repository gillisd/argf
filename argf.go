// Package argf reads the files named on a command line as one stream, with
// standard input in their place when none are named.
//
// # Args and the Reader
//
// A Reader works with its Args slice.
//
//   - Args is the argument vector. Give it the command-line arguments with the
//     options removed, which is flag.Args().
//   - The Reader is the argument files. It opens the inputs Args names and
//     reads them, in order, as one stream.
//
// Each element of Args names an input, and is one of
//
//   - the path of a file to open
//   - "-", standard input
//
// Remove anything else from Args before the Reader reaches it.
//
// # Reading
//
// The Reader consults Args at every read.
//
// When the very first read finds Args empty, the input is standard input, for
// as long as Args stays empty. Otherwise the Reader opens the inputs Args
// names, left to right. Each name leaves Args as its input opens. Each input
// closes as it is exhausted. Inputs open one at a time, on demand, so a Reader
// that is never read opens nothing.
//
// Standard input is read wherever Args holds "-", however many times, and is
// never closed. When Args names files and no "-", standard input is not read.
//
// No single Read returns data from two inputs.
//
// Args is an ordinary slice. Remove a name and that input is skipped. Append
// one and it is read, even after the Reader has reported io.EOF. A Reader that
// started on an empty Args moves to the files if Args gains any.
//
// # Example
//
// With these files:
//
//	$ cat foo.txt
//	Foo 0
//	Foo 1
//	$ cat bar.txt
//	Bar 0
//	Bar 1
//
// this program prints Args, Filename and the line for each line read:
//
//	flag.Parse()
//	f := argf.New(flag.Args()...)
//	sc := bufio.NewScanner(f)
//	for sc.Scan() {
//		fmt.Printf("%q %q %q\n", f.Args, f.Filename(), sc.Text())
//	}
//
// Files are read in order, each name leaving Args as its file opens:
//
//	$ t foo.txt bar.txt
//	["bar.txt"] "foo.txt" "Foo 0"
//	["bar.txt"] "foo.txt" "Foo 1"
//	[] "bar.txt" "Bar 0"
//	[] "bar.txt" "Bar 1"
//
// An empty Args reads standard input:
//
//	$ echo "Open the pod bay doors, Hal." | t
//	[] "-" "Open the pod bay doors, Hal."
//
// So does "-", wherever it appears:
//
//	$ echo "Open the pod bay doors, Hal." | t foo.txt - bar.txt
//	["-" "bar.txt"] "foo.txt" "Foo 0"
//	["-" "bar.txt"] "foo.txt" "Foo 1"
//	["bar.txt"] "-" "Open the pod bay doors, Hal."
//	[] "bar.txt" "Bar 0"
//	[] "bar.txt" "Bar 1"
//
// Without "-", standard input is not read:
//
//	$ echo "Open the pod bay doors, Hal." | t foo.txt
//	[] "foo.txt" "Foo 0"
//	[] "foo.txt" "Foo 1"
//
// # Errors
//
// An input that cannot be opened or read is reported once, as an *fs.PathError
// naming it, along with any data read before the failure. The next read moves
// on to the input after it. A bufio.Scanner stops at the first error. Make a
// new Scanner to continue with the next input. This loop prints every line it
// can and every error it meets:
//
//	for {
//		sc := bufio.NewScanner(f)
//		for sc.Scan() {
//			fmt.Println(sc.Text())
//		}
//		if sc.Err() == nil {
//			break
//		}
//		fmt.Fprintln(os.Stderr, sc.Err())
//	}
//
//	$ t foo.txt missing.txt bar.txt
//	Foo 0
//	Foo 1
//	open missing.txt: no such file or directory
//	Bar 0
//	Bar 1
//
// # Lines, bytes and runes
//
// A Reader is an io.Reader, io.WriterTo and io.Closer. Read lines, bytes and
// runes through a bufio.Scanner or bufio.Reader on top of it. No Read returns
// data from two inputs, and bufio asks for more only when its buffer is empty,
// so when bufio hands out a line, Filename and File name the input that line
// came from. A last line without a newline is joined to the first line of the
// next input.
package argf

import (
	"io"
	"os"
	"slices"
)

// A Reader reads the inputs named in Args, in order, as one stream. The zero
// value reads os.Stdin.
type Reader struct {
	// Args is the argument vector. It holds the inputs not yet opened, each a
	// file path or "-" for standard input. The Reader removes each name as it
	// opens that input and consults Args again at every read.
	Args []string

	current     *os.File
	ownsCurrent bool
	filename    string
	started     bool
	stdinOnly   bool
	closed      bool
}

var (
	_ io.ReadCloser = (*Reader)(nil)
	_ io.WriterTo   = (*Reader)(nil)
)

// New returns a Reader whose Args is a copy of args.
func New(args ...string) *Reader {
	return &Reader{Args: slices.Clone(args)}
}

// Read reads from the open input, opening the next one Args names when it is
// exhausted. No call returns data from two inputs. A failing input is
// reported once, with any data read before the failure, and the next call
// moves past it. Read returns io.EOF when Args is empty and the last input is
// exhausted, and os.ErrClosed after Close.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := r.next(); err != nil {
			return 0, err
		}
		n, err := r.current.Read(p)
		switch {
		case err == nil:
			return n, nil
		case err != io.EOF:
			r.discardCurrent()
			return n, err
		case r.stdinOnly:
			return 0, io.EOF
		}
		if err := r.closeCurrent(); err != nil {
			return 0, err
		}
	}
}

// WriteTo copies the remaining inputs to w, one io.Copy per input. It stops
// at the first error. The next call continues with the input after the one
// that failed.
func (r *Reader) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for {
		if err := r.next(); err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
		n, err := io.Copy(w, r.current)
		total += n
		if err != nil {
			r.discardCurrent()
			return total, err
		}
		if r.stdinOnly {
			return total, nil
		}
		if err := r.closeCurrent(); err != nil {
			return total, err
		}
	}
}

// File returns the open input, os.Stdin included. It is nil before the first
// read and after the last input is exhausted.
func (r *Reader) File() *os.File {
	return r.current
}

// Filename returns the name of the open input, or of the one the next read
// will open. Standard input is named "-". Once the last input is exhausted
// Filename still names that input. It opens nothing.
func (r *Reader) Filename() string {
	switch {
	case r.current != nil:
		return r.filename
	case len(r.Args) > 0:
		return r.Args[0]
	case !r.started:
		return "-"
	default:
		return r.filename
	}
}

// Close closes the open input, unless it is standard input, and empties Args.
// Later reads return os.ErrClosed. A second Close does nothing and returns nil.
func (r *Reader) Close() error {
	r.closed, r.Args = true, nil
	return r.closeCurrent()
}

// next opens the next input when none is open. It returns io.EOF when Args is
// empty and os.ErrClosed after Close.
func (r *Reader) next() error {
	if r.closed {
		return os.ErrClosed
	}
	switch {
	case !r.started:
		r.started = true
		r.stdinOnly = len(r.Args) == 0
	case r.stdinOnly && len(r.Args) > 0:
		r.stdinOnly = false
		r.current = nil
	}
	if r.current != nil {
		return nil
	}
	if r.stdinOnly {
		r.useStdin()
		return nil
	}
	if len(r.Args) == 0 {
		return io.EOF
	}
	name := r.Args[0]
	r.Args = r.Args[1:]
	if name == "-" {
		r.useStdin()
		return nil
	}
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	r.filename, r.current, r.ownsCurrent = name, f, true
	return nil
}

func (r *Reader) useStdin() {
	r.filename, r.current, r.ownsCurrent = "-", os.Stdin, false
}

func (r *Reader) discardCurrent() {
	r.stdinOnly = false
	_ = r.closeCurrent()
}

func (r *Reader) closeCurrent() error {
	f, owned := r.current, r.ownsCurrent
	r.current, r.ownsCurrent = nil, false
	if !owned {
		return nil
	}
	return f.Close()
}
