package errorsf

import (
	"fmt"
	"os"
)

func Check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "synk:", err)
		os.Exit(1)
	}
}

func Fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "synk: "+format+"\n", args...)
	os.Exit(1)
}
