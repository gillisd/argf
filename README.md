# argf

The files named on the command line, read in order as one stream, with
standard input in their place when none are named.

`cat`

```go
f := argf.New(flag.Args()...)
if _, err := io.Copy(os.Stdout, f); err != nil {
	log.Fatal(err)
}
```

`grep`

```go
f := argf.New(flag.Args()...)
sc := bufio.NewScanner(f)
for sc.Scan() {
	if strings.Contains(sc.Text(), pattern) {
		fmt.Printf("%s:%s\n", f.Filename(), sc.Text())
	}
}
if err := sc.Err(); err != nil {
	log.Fatal(err)
}
```

`cat` that reports an unreadable input and carries on

```go
f := argf.New(flag.Args()...)
for {
	_, err := io.Copy(os.Stdout, f)
	if err == nil {
		break
	}
	fmt.Fprintln(os.Stderr, err)
}
```
