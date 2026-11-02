package argf_test

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gillisd/argf"
)

func tempFiles(contents ...string) (paths []string, cleanup func()) {
	dir, err := os.MkdirTemp("", "argf")
	if err != nil {
		log.Fatal(err)
	}
	for i, content := range contents {
		path := filepath.Join(dir, fmt.Sprintf("%c.txt", 'a'+i))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			log.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths, func() { os.RemoveAll(dir) }
}

// Example is cat. It copies the inputs, in order, to stdout.
func Example() {
	paths, cleanup := tempFiles("alpha\n", "beta\n")
	defer cleanup()

	if _, err := io.Copy(os.Stdout, argf.New(paths...)); err != nil {
		log.Fatal(err)
	}
	// Output:
	// alpha
	// beta
}

// ExampleReader_Filename is grep -n. A Scanner splits the lines and Filename
// names the input each came from.
func ExampleReader_Filename() {
	paths, cleanup := tempFiles("alpha\nomega\n", "beta\n")
	defer cleanup()

	f := argf.New(paths...)
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		if strings.Contains(sc.Text(), "a") {
			fmt.Printf("%s:%d:%s\n", filepath.Base(f.Filename()), n, sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
	// Output:
	// a.txt:1:alpha
	// a.txt:2:omega
	// b.txt:3:beta
}

// ExampleReader_File reads the size of each input from the file behind the
// line a Scanner just produced.
func ExampleReader_File() {
	paths, cleanup := tempFiles("alpha\nomega\n", "beta\n")
	defer cleanup()

	f := argf.New(paths...)
	sc := bufio.NewScanner(f)
	heading := ""
	for sc.Scan() {
		if f.Filename() != heading {
			heading = f.Filename()
			info, err := f.File().Stat()
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("==> %s (%d bytes) <==\n", filepath.Base(heading), info.Size())
		}
		fmt.Println(sc.Text())
	}
	// Output:
	// ==> a.txt (12 bytes) <==
	// alpha
	// omega
	// ==> b.txt (5 bytes) <==
	// beta
}
