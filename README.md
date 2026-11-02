# argf

The files named on the command line, read in order as one stream, with
standard input in their place when none are named.

`cat`

```go
if _, err := io.Copy(os.Stdout, argf.Default); err != nil {
	log.Fatal(err)
}
```

`grep`

```go
pattern := flag.String("e", "", "pattern to match")
flag.Parse()

sc := bufio.NewScanner(argf.Default)
for sc.Scan() {
	if strings.Contains(sc.Text(), *pattern) {
		fmt.Printf("%s:%s\n", argf.Default.Filename(), sc.Text())
	}
}
if err := sc.Err(); err != nil {
	log.Fatal(err)
}
```

`cat` that reports an unreadable input and carries on

```go
for {
	_, err := io.Copy(os.Stdout, argf.Default)
	if err == nil {
		break
	}
	fmt.Fprintln(os.Stderr, err)
}
```
