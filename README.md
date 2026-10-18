# unsparse 

A simple package to copy sparse files into an alternative (non-sparse) binary 
format. 

### Overview 

Some tools, including [Go's own tar.Writer](https://github.com/golang/go/issues/13548) don't handle sparse files correctly. 

unsparse provides a work around for this by allowing you to re-write your sparse
file as an "unsparsed" file. This "unsparsed" file is simply your sparse file 
with the holes removed.

Although your system won't correctly recognise this file as a sparse file you 
can use this package to efficiently convert between these formats to work around
tooling that lacks sparse file support.

### Usage 

For library usage, please see the [Go pkg.go.dev](https://pkg.go.dev/github.com/alexbathome/unsparse) docs. 

To use unsparse via the CLI, please see the [Unspar tool](https://github.com/alexbathome/unspar)