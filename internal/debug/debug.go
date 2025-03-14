// +build debug

package debug

import "fmt"

func Println(args ...interface{}) {
    fmt.Println(args...)
}