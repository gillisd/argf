package argf_test

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
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

var noArgs = []string{}

type outcome struct {
	data string
	err  error
}

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

func commandLine(t *testing.T, args ...string) {
	t.Helper()
	osArgs, flags, def := os.Args, flag.CommandLine, argf.Default
	os.Args = append([]string{"test"}, args...)
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	argf.Default = new(argf.Reader)
	t.Cleanup(func() { os.Args, flag.CommandLine, argf.Default = osArgs, flags, def })
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

func read(r io.Reader, n int) outcome {
	buf := make([]byte, n)
	k, err := r.Read(buf)
	return outcome{string(buf[:k]), err}
}

func readAll(r io.Reader) outcome {
	data, err := io.ReadAll(r)
	return outcome{string(data), err}
}

func copyAll(r io.Reader) outcome {
	var out bytes.Buffer
	_, err := io.Copy(&out, r)
	return outcome{out.String(), err}
}

func advance(r io.Reader, n int) {
	io.ReadFull(r, make([]byte, n))
}

func drain(r io.Reader) {
	io.Copy(io.Discard, r)
}

func scan(sc *bufio.Scanner) string {
	sc.Scan()
	return sc.Text()
}

func pathOf(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Path
	}
	return ""
}

func isClosed(f *os.File) bool {
	_, err := f.Read(make([]byte, 1))
	return errors.Is(err, os.ErrClosed)
}

func TestReader_Read(t *testing.T) {
	t.Run("concatenates the named files in order", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		equal(t, readAll(argf.New(paths...)), outcome{"alpha\nbeta\n", nil})
	})

	t.Run("reads stdin when nothing is named", func(t *testing.T) {
		stdin(t, "from stdin\n")
		equal(t, readAll(argf.New(noArgs...)), outcome{"from stdin\n", nil})
	})

	t.Run("reads stdin where - is named", func(t *testing.T) {
		stdin(t, "stdin\n")
		paths := files(t, "alpha\n", "beta\n")
		equal(t, readAll(argf.New(paths[0], "-", paths[1])), outcome{"alpha\nstdin\nbeta\n", nil})
	})

	t.Run("reads a file named twice twice", func(t *testing.T) {
		paths := files(t, "alpha\n")
		equal(t, readAll(argf.New(paths[0], paths[0])), outcome{"alpha\nalpha\n", nil})
	})

	t.Run("passes through empty inputs", func(t *testing.T) {
		paths := files(t, "", "beta\n")
		equal(t, readAll(argf.New(paths...)), outcome{"beta\n", nil})
	})

	t.Run("satisfies the io.Reader contract", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		equal(t, iotest.TestReader(argf.New(paths...), []byte("alpha\nbeta\n")), nil)
	})

	t.Run("returns only the first input from the first call", func(t *testing.T) {
		f := argf.New(files(t, "ab", "cd")...)
		equal(t, read(f, 16), outcome{"ab", nil})
	})

	t.Run("returns the second input from the second call", func(t *testing.T) {
		f := argf.New(files(t, "ab", "cd")...)
		read(f, 16)
		equal(t, read(f, 16), outcome{"cd", nil})
	})

	t.Run("opens the first input on the first call", func(t *testing.T) {
		paths := files(t, "alpha\n")
		f := argf.New(paths...)
		read(f, 1)
		equal(t, f.File().Name(), paths[0])
	})

	t.Run("closes an input once it is exhausted", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		sc := bufio.NewScanner(f)
		scan(sc)
		first := f.File()
		scan(sc)
		equal(t, isClosed(first), true)
	})

	t.Run("returns io.EOF once every input is consumed", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		drain(f)
		equal(t, read(f, 8), outcome{"", io.EOF})
	})

	t.Run("opens files appended to Args after the stream was exhausted", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0])
		drain(f)
		f.Args = append(f.Args, paths[1])
		equal(t, readAll(f), outcome{"beta\n", nil})
	})

	t.Run("switches from a lone stdin to files appended to Args", func(t *testing.T) {
		stdin(t, "one\ntwo\n")
		paths := files(t, "alpha\n")
		f := argf.New(noArgs...)
		advance(f, len("one\n"))
		f.Args = append(f.Args, paths[0])
		equal(t, readAll(f), outcome{"alpha\n", nil})
	})

	t.Run("reads a lone stdin again after it reports io.EOF", func(t *testing.T) {
		in := stdin(t, "a")
		f := argf.New(noArgs...)
		drain(f)
		if err := os.WriteFile(in.Name(), []byte("ab"), 0o644); err != nil {
			t.Fatal(err)
		}
		equal(t, readAll(f), outcome{"b", nil})
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

	t.Run("joins an unterminated last line to the next input's first line", func(t *testing.T) {
		sc := bufio.NewScanner(argf.New(files(t, "x", "y\n")...))
		equal(t, scan(sc), "xy")
	})
}

func TestReader_errors(t *testing.T) {
	t.Run("reports an open error from the call that reaches the input", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		read(f, 16)
		equal(t, errors.Is(read(f, 16).err, fs.ErrNotExist), true)
	})

	t.Run("names the file an open error is about", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		equal(t, pathOf(read(argf.New(missing), 1).err), missing)
	})

	t.Run("continues with the next input after an open error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		read(f, 16)
		read(f, 16)
		equal(t, read(f, 16), outcome{"beta\n", nil})
	})

	t.Run("names the input a read error is about", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		dir := t.TempDir()
		f := argf.New(paths[0], dir, paths[1])
		read(f, 16)
		equal(t, pathOf(read(f, 16).err), dir)
	})

	t.Run("closes an input that fails to read", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])
		read(f, 16)
		read(f, 16)
		equal(t, f.File(), nil)
	})

	t.Run("continues with the next input after a read error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])
		read(f, 16)
		read(f, 16)
		equal(t, read(f, 16), outcome{"beta\n", nil})
	})

	t.Run("reports a failing lone stdin", func(t *testing.T) {
		stdin(t, "").Close()
		equal(t, errors.Is(read(argf.New(noArgs...), 1).err, os.ErrClosed), true)
	})

	t.Run("returns io.EOF after reporting a failing lone stdin", func(t *testing.T) {
		stdin(t, "").Close()
		f := argf.New(noArgs...)
		read(f, 1)
		equal(t, read(f, 8), outcome{"", io.EOF})
	})

	t.Run("stops a bufio.Scanner at the error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		sc := bufio.NewScanner(argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1]))
		scan(sc)
		equal(t, sc.Scan(), false)
	})

	t.Run("hands the error to the bufio.Scanner", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		sc := bufio.NewScanner(argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1]))
		for sc.Scan() {
		}
		equal(t, errors.Is(sc.Err(), fs.ErrNotExist), true)
	})

	t.Run("continues with the next input under a fresh bufio.Scanner", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		for sc := bufio.NewScanner(f); sc.Scan(); {
		}
		equal(t, scan(bufio.NewScanner(f)), "beta")
	})
}

func TestReader_File(t *testing.T) {
	t.Run("is nil before the first read", func(t *testing.T) {
		equal(t, argf.New(files(t, "alpha\n")...).File(), nil)
	})

	t.Run("is the open file", func(t *testing.T) {
		paths := files(t, "alpha\n")
		f := argf.New(paths...)
		read(f, 1)
		equal(t, f.File().Name(), paths[0])
	})

	t.Run("is os.Stdin for standard input", func(t *testing.T) {
		in := stdin(t, "stdin\n")
		f := argf.New(noArgs...)
		read(f, 1)
		equal(t, f.File(), in)
	})

	t.Run("is nil once the stream is exhausted", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		drain(f)
		equal(t, f.File(), nil)
	})

	t.Run("is the file behind each line a Scanner yields", func(t *testing.T) {
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
		equal(t, argf.New(paths[0], "missing").Filename(), paths[0])
	})

	t.Run("names stdin - before anything is read when nothing is named", func(t *testing.T) {
		equal(t, argf.New(noArgs...).Filename(), "-")
	})

	t.Run("names stdin - while reading it", func(t *testing.T) {
		stdin(t, "stdin\n")
		f := argf.New(noArgs...)
		read(f, 1)
		equal(t, f.Filename(), "-")
	})

	t.Run("names stdin - where - is named", func(t *testing.T) {
		stdin(t, "stdin\n")
		f := argf.New("-", files(t, "alpha\n")[0])
		read(f, 1)
		equal(t, f.Filename(), "-")
	})

	t.Run("names the next input after a failure", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])
		advance(f, len("alpha\n"))
		read(f, 1)
		equal(t, f.Filename(), paths[1])
	})
}

func TestReader_Close(t *testing.T) {
	t.Run("closes the current input", func(t *testing.T) {
		f := argf.New(files(t, "alpha\nomega\n")...)
		read(f, 1)
		file := f.File()
		f.Close()
		equal(t, isClosed(file), true)
	})

	t.Run("returns nil", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		read(f, 1)
		equal(t, f.Close(), nil)
	})

	t.Run("drops the remaining Args", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		read(f, 1)
		f.Close()
		equal(t, len(f.Args), 0)
	})

	t.Run("fails later reads with os.ErrClosed", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		read(f, 1)
		f.Close()
		equal(t, errors.Is(read(f, 1).err, os.ErrClosed), true)
	})

	t.Run("fails later copies with os.ErrClosed", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		read(f, 1)
		f.Close()
		equal(t, errors.Is(copyAll(f).err, os.ErrClosed), true)
	})

	t.Run("never closes stdin", func(t *testing.T) {
		in := stdin(t, "one\n")
		f := argf.New(noArgs...)
		read(f, 1)
		f.Close()
		equal(t, isClosed(in), false)
	})

	t.Run("returns nil when called twice", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		read(f, 1)
		f.Close()
		equal(t, f.Close(), nil)
	})

	t.Run("fails later reads with os.ErrClosed when called before the first read", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		f.Close()
		equal(t, errors.Is(read(f, 1).err, os.ErrClosed), true)
	})

	t.Run("returns the error from closing the current input", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n")...)
		read(f, 1)
		f.File().Close()
		equal(t, errors.Is(f.Close(), os.ErrClosed), true)
	})
}

func TestReader_WriteTo(t *testing.T) {
	t.Run("copies every input through io.Copy", func(t *testing.T) {
		stdin(t, "stdin\n")
		paths := files(t, "alpha\n", "beta\n")
		equal(t, copyAll(argf.New(paths[0], "-", paths[1])), outcome{"alpha\nstdin\nbeta\n", nil})
	})

	t.Run("leaves no input open once every input is copied", func(t *testing.T) {
		f := argf.New(files(t, "alpha\n", "beta\n")...)
		copyAll(f)
		equal(t, f.File(), nil)
	})

	t.Run("copies the remainder after partial reads", func(t *testing.T) {
		f := argf.New(files(t, "alpha\nomega\n", "beta\n")...)
		advance(f, len("alpha\n"))
		equal(t, copyAll(f), outcome{"omega\nbeta\n", nil})
	})

	t.Run("copies a lone stdin", func(t *testing.T) {
		stdin(t, "stdin\n")
		equal(t, copyAll(argf.New(noArgs...)), outcome{"stdin\n", nil})
	})

	t.Run("copies what comes before an input that fails to open", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		equal(t, copyAll(f).data, "alpha\n")
	})

	t.Run("reports the open error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		equal(t, errors.Is(copyAll(f).err, fs.ErrNotExist), true)
	})

	t.Run("continues with the next input on the call after an open error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], filepath.Join(t.TempDir(), "missing"), paths[1])
		copyAll(f)
		equal(t, copyAll(f), outcome{"beta\n", nil})
	})

	t.Run("copies what comes before an input that fails to read", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])
		equal(t, copyAll(f).data, "alpha\n")
	})

	t.Run("reports the read error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		dir := t.TempDir()
		f := argf.New(paths[0], dir, paths[1])
		equal(t, pathOf(copyAll(f).err), dir)
	})

	t.Run("continues with the next input on the call after a read error", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		f := argf.New(paths[0], t.TempDir(), paths[1])
		copyAll(f)
		equal(t, copyAll(f), outcome{"beta\n", nil})
	})
}

func TestReader_commandLine(t *testing.T) {
	t.Run("Default names the first file on the command line before anything is read", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		commandLine(t, paths...)
		equal(t, argf.Default.Filename(), paths[0])
	})

	t.Run("Default reads the files named on the command line", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		commandLine(t, paths...)
		equal(t, readAll(argf.Default), outcome{"alpha\nbeta\n", nil})
	})

	t.Run("Default names stdin - when the command line names nothing", func(t *testing.T) {
		commandLine(t)
		equal(t, argf.Default.Filename(), "-")
	})

	t.Run("Default reads stdin when the command line names nothing", func(t *testing.T) {
		commandLine(t)
		stdin(t, "stdin\n")
		equal(t, readAll(argf.Default), outcome{"stdin\n", nil})
	})

	t.Run("Default reads flag.Args once flag.Parse has run", func(t *testing.T) {
		paths := files(t, "alpha\n")
		commandLine(t, "-v", paths[0])
		flag.Bool("v", false, "")
		flag.Parse()
		equal(t, readAll(argf.Default), outcome{"alpha\n", nil})
	})

	t.Run("New reads the operands it is given instead of the command line", func(t *testing.T) {
		paths := files(t, "alpha\n", "beta\n")
		commandLine(t, "pattern", paths[1])
		equal(t, readAll(argf.New(os.Args[2:]...)), outcome{"beta\n", nil})
	})

	t.Run("New with no operands reads stdin", func(t *testing.T) {
		commandLine(t, files(t, "alpha\n")...)
		stdin(t, "stdin\n")
		equal(t, readAll(argf.New(noArgs...)), outcome{"stdin\n", nil})
	})

	t.Run("New with no arguments reads the command line", func(t *testing.T) {
		paths := files(t, "alpha\n")
		commandLine(t, paths...)
		equal(t, readAll(argf.New()), outcome{"alpha\n", nil})
	})

	t.Run("the zero value reads the command line", func(t *testing.T) {
		paths := files(t, "alpha\n")
		commandLine(t, paths...)
		var f argf.Reader
		equal(t, readAll(&f), outcome{"alpha\n", nil})
	})

	t.Run("New copies its arguments", func(t *testing.T) {
		args := []string{"a", "b"}
		f := argf.New(args...)
		args[0] = "changed"
		equal(t, f.Filename(), "a")
	})
}
