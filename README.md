# filedown

> Concurrent block download of files


# Use

> go get github.com/yanmengfei/filedown


```go
package main

import (
    "fmt"

    "github.com/yanmengfei/filedown"
)

func main() {
    var core = filedown.New("https://ftp.afrinic.net/pub/dbase/afrinic.db.gz") // new object
    core.SetConcurrency(24) // set concurrency number
    core.SetOutput("./afrinic.db.gz") // set output filepath
    if err := core.Do(); err != nil {
        panic(err)
    }
}

```
