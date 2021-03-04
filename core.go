package filedown

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"
)

var queue, redo, finish chan int

type model struct {
	URL         string
	Concurrency int
	ChunkSize   int
	Output      string
	Timeout     time.Duration
	filesize    int
}

func New(url string) *model {
	return &model{URL: url}
}

func (m *model) SetConcurrency(con int) {
	m.Concurrency = con
}

func (m *model) SetChunk(size int) {
	m.ChunkSize = size
}

func (m *model) SetOutput(dst string) {
	m.Output = dst
}

func (m *model) SetTimeout(t time.Duration) {
	if t < 2*time.Second {
		t = 5 * time.Second
	}
	m.Timeout = t
}

func (m *model) Do() error {
	var startTime = time.Now()
	var client = http.Client{Timeout: m.Timeout}
	request, err := http.NewRequest("GET", m.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	if err = response.Body.Close(); err != nil {
		return err
	}
	var length = response.Header.Get("Content-Length")
	m.filesize, _ = strconv.Atoi(length)

	ranges := response.Header.Get("Accept-Ranges")
	log.Println("Filesize:", m.filesize, "\tRanges:", ranges)
	if m.Concurrency < 1 {
		m.SetConcurrency(24)
	}

	if m.ChunkSize < 1 {
		m.SetChunk(int(math.Ceil(float64(m.filesize) / float64(m.Concurrency))))
	}
	var fragment = int(math.Ceil(float64(m.filesize) / float64(m.ChunkSize)))
	queue = make(chan int, m.Concurrency)
	redo = make(chan int, int(math.Floor(float64(m.Concurrency)/2)))
	go func() {
		for i := 0; i < fragment; i++ {
			queue <- i
		}
		// redo: 某块下载失败了，重新投递到queue
		for {
			j := <-redo
			queue <- j
		}
	}()
	finish = make(chan int, m.Concurrency)
	for j := 0; j < m.Concurrency; j++ {
		go m.do(request, fragment, j)
	}

	//finish的目的：等分块fragment都完成了，主进程接着往下执行。如果没有这个，那么主进程不会等子协程结束就会提前退出
	for k := 0; k < fragment; k++ {
		_ = <-finish
	}
	log.Println("Start to combine files...")
	file, err := os.Create(m.Output)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var offset int64 = 0
	// 合并分块下载的多个文件
	for x := 0; x < fragment; x++ {
		var filename = fmt.Sprintf("%s_%d", m.Output, x)
		buf, err := ioutil.ReadFile(filename)
		if err != nil {
			log.Println(err)
			continue
		}
		_, _ = file.WriteAt(buf, offset)
		offset += int64(len(buf))
		_ = os.Remove(filename)
	}
	log.Println("Written to ", m.Output)
	var cost = time.Now().Sub(startTime).Seconds()
	log.Printf("Cost: %f\nSpeed: %f Kb/s\n", cost, float64(m.filesize)/cost/1024)
	return nil
}

func deepcopy(dst, src interface{}) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(src); err != nil {
		return err
	}
	return gob.NewDecoder(bytes.NewBuffer(buf.Bytes())).Decode(dst)
}

func (m *model) do(request *http.Request, fragment, no int) {
	var req http.Request
	err := deepcopy(&req, request)
	if err != nil {
		log.Println("ERROR|prepare request:", err)
		log.Panic(err)
		return
	}
	for {
		cStartTime := time.Now()

		i := <-queue
		start := i * m.ChunkSize
		var end int
		if i < fragment-1 {
			end = start + m.ChunkSize - 1
		} else {
			end = m.filesize - 1
		}
		filename := fmt.Sprintf("%s_%d", m.Output, i)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		log.Printf("[%d][%d]Start download:%d-%d\n", no, i, start, end)
		cli := http.Client{Timeout: m.Timeout}
		resp, err := cli.Do(&req)
		if err != nil {
			log.Printf("[%d][%d]ERROR|do request:%s\n", no, i, err.Error())
			redo <- i
			continue
		}

		log.Printf("[%d][%d]Content-Range:%s\n", no, i, resp.Header.Get("Content-Range"))

		file, err := os.Create(filename)
		if err != nil {
			log.Printf("[%d][%d]ERROR|create file %s:%s\n", no, i, filename, err.Error())
			_ = file.Close()
			_ = resp.Body.Close()
			redo <- i
			continue
		}
		log.Printf("[%d][%d]Writing to file %s\n", no, i, filename)
		n, err := io.Copy(file, resp.Body)
		if err != nil {
			log.Printf("[%d][%d]ERROR|write to file %s:%s\n", no, i, filename, err.Error())
			_ = file.Close()
			_ = resp.Body.Close()
			redo <- i
			continue
		}
		cEndTime := time.Now()
		duration := cEndTime.Sub(cStartTime).Seconds()
		log.Printf("[%d][%d]Download successfully:%f Kb/s\n", no, i, float64(n)/duration/1024)

		_ = file.Close()
		_ = resp.Body.Close()

		finish <- i
	}
}
