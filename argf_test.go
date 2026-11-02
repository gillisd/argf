package argf_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/iotest"

	"github.com/gillisd/argf"
)

func files(t *testing.T, contents ...string) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, len(contents))
	for i, content := range contents {
		paths[i] = filepath.Join(dir, fmt.Sprintf("file%d", i))
		if err := os.WriteFile(paths[i], []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func stdin(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = original
		f.Close()
	})
	return f
}

func equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func equalSlices[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(data)
}

func scan(t *testing.T, sc *bufio.Scanner) string {
	t.Helper()
	if !sc.Scan() {
		t.Fatalf("Scan: %v", sc.Err())
	}
	return sc.Text()
}

func expectEOF(t *testing.T, r io.Reader) {
	t.Helper()
	n, err := r.Read(make([]byte, 8))
	if n != 0 || err != io.EOF {
		t.Fatalf("Read = %d, %v; want 0, io.EOF", n, err)
	}
}

func isClosed(f *os.File) bool {
	_, err := f.Read(make([]byte, 1))
	return errors.Is(err, os.ErrClosed)
}

func TestReader_Read(t *testing.T) {
	t.Run("concatenates the named files in order", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		equal(t, readAll(t, argf.New(paths...)), "alpha\nbeta\n")
	})

	t.Run("reads stdin when nothing is named", func(t *testing.T) {
		stdin(t, "from stdin\n")
		equal(t, readAll(t, argf.New()), "from stdin\n")
	})

	t.Run("reads stdin where - is named", func(t *testing.T) {
		stdin(t, "stdin\n")
		paths := files(t, "alpha\n", "beta\n")
		equal(t, readAll(t, argf.New(paths[0], "-", paths[1])), "alpha\nstdin\nbeta\n")
	})

	t.Run("reads a file named twice twice", func(t *testing.T) {
		paths := files(t, "alpha\n")
		equal(t, readAll(t, argf.New(paths[0], paths[0])), "alpha\nalpha\n")
	})

	t.Run("passes through empty inputs", func(t *testing.T) {
		paths := files(t, "", "beta\n")
		equal(t, readAll(t, argf.New(paths...)), "beta\n")
	})

	t.Run("satisfies the io.Reader contract", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		if err := iotest.TestReader(argf.New(paths...), []byte("alpha\nbeta\n")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("never spans two inputs in one call", func(t *testing.T) {
		f := argf.New(files(t, "ab", "cd")...)
		buf := make([]byte, 16)

		n, err := f.Read(buf)
		equal(t, string(buf[:n]), "ab")
		equal(t, err, nil)

		n, err = f.Read(buf)
		equal(t, string(buf[:n]), "cd")
		equal(t, err, nil)
	})

	t.Run("opens nothing until the first read", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		equal(t, f.File(), nil)

		f.Read(make([]byte, 1))
		equal(t, f.File() != nil, true)
	})

	t.Run("opens inputs one at a time and closes each as it is exhausted", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths...)
		sc := bufio.NewScanner(f)

		scan(t, sc)
		first := f.File()
		equal(t, first.Name(), paths[0])

		scan(t, sc)
		equal(t, isClosed(first), true)
		equal(t, f.File().Name(), paths[1])

		equal(t, sc.Scan(), false)
		equal(t, f.File(), nil)
	})

	t.Run("returns io.EOF only once every input is consumed", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		readAll(t, f)
		expectEOF(t, f)
	})

	t.Run("opens files appended to Args after the stream was exhausted", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0])
		equal(t, readAll(t, f), "alpha\n")

		f.Args = append(f.Args, paths[1])
		equal(t, readAll(t, f), "beta\n")
	})

	t.Run("switches from a lone stdin to files appended to Args", func(t *testing.T) {
		stdin(t, "one\ntwo\n")
		paths := files(t, "alpha\n")
		f := argf.New()
		buf := make([]byte, len("one\n"))
		io.ReadFull(f, buf)
		equal(t, string(buf), "one\n")

		f.Args = append(f.Args, paths[0])
		equal(t, readAll(t, f), "alpha\n")
	})

	t.Run("reads a lone stdin again after it reports io.EOF", func(t *testing.T) {
		in := stdin(t, "a")
		f := argf.New()
		equal(t, readAll(t, f), "a")

		if err := os.WriteFile(in.Name(), []byte("ab"), 0o644); err != nil {
			t.Fatal(err)
		}
		equal(t, readAll(t, f), "b")
	})
}

func TestReader_underBufio(t *testing.T) {
	t.Run("a Scanner sees Filename change exactly at input boundaries", func(t *testing.T) {
		paths := files(t, "1\n2\n", "3\n4\n")
		f := argf.New(paths...)
		sc := bufio.NewScanner(f)

		names := []string{f.Filename()}
		for sc.Scan() {
			names = append(names, f.Filename())
		}
		names = append(names, f.Filename())

		equalSlices(t, names, []string{paths[0], paths[0], paths[0], paths[1], paths[1], paths[1]})
	})

	t.Run("a bufio.Reader sees Filename change exactly at input boundaries", func(t *testing.T) {
		paths := files(t, "1\n2\n", "3\n4\n")
		f := argf.New(paths...)
		br := bufio.NewReader(f)

		var names []string
		for {
			if _, err := br.ReadString('\n'); err != nil {
				break
			}
			names = append(names, f.Filename())
		}
		equalSlices(t, names, []string{paths[0], paths[0], paths[1], paths[1]})
	})

	t.Run("runs an unterminated last line into the next input, as cat does", func(t *testing.T) {
		sc := bufio.NewScanner(argf.New(files(t, "x", "y\n")...))
		equal(t, scan(t, sc), "xy")
	})
}

func TestReader_errors(t *testing.T) {
	t.Run("an open error consumes the name and the next read continues", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		buf := make([]byte, 16)

		n, _ := f.Read(buf)
		equal(t, string(buf[:n]), "alpha\n")

		_, err := f.Read(buf)
		equal(t, errors.Is(err, fs.ErrNotExist), true)

		n, _ = f.Read(buf)
		equal(t, string(buf[:n]), "beta\n")
	})

	t.Run("a read error is reported once, closing that input, and the next read continues", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		dir := t.TempDir()
		f := argf.New(paths[0], dir, paths[1])
		buf := make([]byte, 16)

		n, _ := f.Read(buf)
		equal(t, string(buf[:n]), "alpha\n")

		_, err := f.Read(buf)
		var pathErr *fs.PathError
		equal(t, errors.As(err, &pathErr), true)
		equal(t, pathErr.Path, dir)
		equal(t, f.File(), nil)

		n, _ = f.Read(buf)
		equal(t, string(buf[:n]), "beta\n")
	})

	t.Run("a failing lone stdin is reported once, then io.EOF", func(t *testing.T) {
		in := stdin(t, "")
		in.Close()
		f := argf.New()

		_, err := f.Read(make([]byte, 1))
		equal(t, errors.Is(err, os.ErrClosed), true)
		expectEOF(t, f)
	})

	t.Run("a bufio.Scanner stops at the error and a fresh one continues", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])

		sc := bufio.NewScanner(f)
		equal(t, scan(t, sc), "alpha")
		equal(t, sc.Scan(), false)
		equal(t, errors.Is(sc.Err(), fs.ErrNotExist), true)

		equal(t, scan(t, bufio.NewScanner(f)), "beta")
	})

	t.Run("names the failing file in the error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		_, err := argf.New(missing).Read(make([]byte, 1))

		var pathErr *fs.PathError
		equal(t, errors.As(err, &pathErr), true)
		equal(t, pathErr.Path, missing)
	})
}

func TestReader_File(t *testing.T) {
	t.Run("is nil before the first read", func(t *testing.T) {
		equal(t, argf.New(files(t, "alpha\n")...).File(), nil)
	})

	t.Run("is the open file", func(t *testing.T) {
		paths := files(t, "alpha\n")
		f := argf.New(paths...)
		f.Read(make([]byte, 1))
		equal(t, f.File().Name(), paths[0])
	})

	t.Run("is os.Stdin for standard input", func(t *testing.T) {
		in := stdin(t, "stdin\n")
		f := argf.New()
		f.Read(make([]byte, 1))
		equal(t, f.File(), in)
	})

	t.Run("is nil once the stream is exhausted", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		readAll(t, f)
		equal(t, f.File(), nil)
	})

	t.Run("is the open file whenever a Scanner above it yields a line", func(t *testing.T) {
		paths := files(t, "1\n2\n", "3\n4\n")
		f := argf.New(paths...)
		sc := bufio.NewScanner(f)

		var names []string
		for sc.Scan() {
			names = append(names, f.File().Name())
		}
		equalSlices(t, names, []string{paths[0], paths[0], paths[1], paths[1]})
	})
}

func TestReader_Filename(t *testing.T) {
	t.Run("names the first input before anything is read", func(t *testing.T) {
		paths := files(t, "alpha\n")
		f := argf.New(paths[0], "missing")
		equal(t, f.Filename(), paths[0])
		equal(t, f.File(), nil)
	})

	t.Run("names stdin - when nothing is named", func(t *testing.T) {
		stdin(t, "stdin\n")
		f := argf.New()
		equal(t, f.Filename(), "-")
		f.Read(make([]byte, 1))
		equal(t, f.Filename(), "-")
	})

	t.Run("names stdin - where - is named", func(t *testing.T) {
		stdin(t, "stdin\n")
		f := argf.New("-", files(t, "alpha\n")[0])
		f.Read(make([]byte, 1))
		equal(t, f.Filename(), "-")
	})

	t.Run("names the next input after a failure", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])
		io.ReadFull(f, make([]byte, len("alpha\n")))
		f.Read(make([]byte, 1))
		equal(t, f.Filename(), paths[1])
	})
}

func TestReader_Close(t *testing.T) {
	t.Run("closes the current input", func(t *testing.T) {
		f := argf.New(files(t, "alpha\nomega\n")...)
		f.Read(make([]byte, 1))
		file := f.File()
		equal(t, f.Close(), nil)
		equal(t, isClosed(file), true)
	})

	t.Run("drops the remaining Args and fails later reads with os.ErrClosed", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		f.Read(make([]byte, 1))
		equal(t, f.Close(), nil)
		equal(t, len(f.Args), 0)

		_, err := f.Read(make([]byte, 1))
		equal(t, errors.Is(err, os.ErrClosed), true)

		_, err = io.Copy(io.Discard, f)
		equal(t, errors.Is(err, os.ErrClosed), true)
	})

	t.Run("never closes stdin", func(t *testing.T) {
		in := stdin(t, "one\n")
		f := argf.New()
		f.Read(make([]byte, 1))
		equal(t, f.Close(), nil)
		equal(t, isClosed(in), false)
	})

	t.Run("is idempotent", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		f.Read(make([]byte, 1))
		equal(t, f.Close(), nil)
		equal(t, f.Close(), nil)
	})

	t.Run("is safe before the first read", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		equal(t, f.Close(), nil)
		_, err := f.Read(make([]byte, 1))
		equal(t, errors.Is(err, os.ErrClosed), true)
	})

	t.Run("returns the error from closing the current input", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		f.Read(make([]byte, 1))
		f.File().Close()
		equal(t, errors.Is(f.Close(), os.ErrClosed), true)
	})
}

func TestReader_WriteTo(t *testing.T) {
	t.Run("copies every input through io.Copy", func(t *testing.T) {
		stdin(t, "stdin\n")
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], "-", paths[1])

		var out bytes.Buffer
		n, err := io.Copy(&out, f)
		equal(t, err, nil)
		equal(t, n, int64(len("alpha\nstdin\nbeta\n")))
		equal(t, out.String(), "alpha\nstdin\nbeta\n")
		equal(t, f.File(), nil)
	})

	t.Run("copies the remainder after partial reads", func(t *testing.T) {
		f := argf.New(files(t, "alpha\nomega\n", "beta\n")...)
		buf := make([]byte, len("alpha\n"))
		io.ReadFull(f, buf)

		var out bytes.Buffer
		_, err := io.Copy(&out, f)
		equal(t, err, nil)
		equal(t, out.String(), "omega\nbeta\n")
	})

	t.Run("copies a lone stdin", func(t *testing.T) {
		stdin(t, "stdin\n")
		var out bytes.Buffer
		_, err := io.Copy(&out, argf.New())
		equal(t, err, nil)
		equal(t, out.String(), "stdin\n")
	})

	t.Run("reports an open error after copying what came before it, then continues", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])

		var out bytes.Buffer
		_, err := io.Copy(&out, f)
		equal(t, errors.Is(err, fs.ErrNotExist), true)
		equal(t, out.String(), "alpha\n")

		_, err = io.Copy(&out, f)
		equal(t, err, nil)
		equal(t, out.String(), "alpha\nbeta\n")
	})

	t.Run("reports a read error, then continues with the next input on the next call", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])

		var out bytes.Buffer
		_, err := io.Copy(&out, f)
		equal(t, err != nil, true)
		equal(t, out.String(), "alpha\n")

		_, err = io.Copy(&out, f)
		equal(t, err, nil)
		equal(t, out.String(), "alpha\nbeta\n")
	})
}

func TestReader_construction(t *testing.T) {
	t.Run("the zero value reads os.Stdin", func(t *testing.T) {
		stdin(t, "zero\n")
		var f argf.Reader
		equal(t, readAll(t, &f), "zero\n")
	})

	t.Run("the zero value with Args reads files", func(t *testing.T) {
		f := argf.Reader{Args: files(t, "alpha\n")}
		equal(t, readAll(t, &f), "alpha\n")
	})

	t.Run("New copies its arguments", func(t *testing.T) {
		args := []string{"a", "b"}
		f := argf.New(args...)
		args[0] = "changed"
		equal(t, f.Filename(), "a")
	})
}
