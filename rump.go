package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/garyburd/redigo/redis"
)

// Report all errors to stdout.
func handle(err error) {
	if err != nil && err != redis.ErrNil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// Scan and queue source keys.
func get(conn redis.Conn, queue chan<- map[string]string) {
	var (
		cursor int64
		keys   []string
	)

	for {
		// Scan a batch of keys.
		values, err := redis.Values(conn.Do("SCAN", cursor))
		handle(err)
		values, err = redis.Scan(values, &cursor, &keys)
		handle(err)

		// Get pipelined dumps.
		for _, key := range keys {
			conn.Send("DUMP", key)
		}
		dumps, err := redis.Strings(conn.Do(""))
		handle(err)

		// Build batch map.
		batch := make(map[string]string)
		for i := range keys {
			batch[keys[i]] = dumps[i]
		}

		// Last iteration of scan.
		if cursor == 0 {
			// queue last batch.
			select {
			case queue <- batch:
			}
			close(queue)
			break
		}

		fmt.Printf(">")
		// queue current batch.
		queue <- batch
	}
}

// Restore a batch of keys on destination.
func put(conn redis.Conn, queue <-chan map[string]string) {
	for batch := range queue {
		for key, value := range batch {
			conn.Send("RESTORE", key, "0", value)
		}
		_, err := conn.Do("")
		handle(err)

		fmt.Printf(".")
	}
}

func main() {
	from := flag.String("from", "", "example: redis://127.0.0.1:6379/0")
	to := flag.String("to", "", "example: redis://127.0.0.1:6379/1")
	flag.Parse()

	if *from == "" || *to == "" {
		flag.Usage()
		return
	}

	// empty passwords are skipped in redigo
	sourceOptions := redis.DialPassword("")
	if sourcePassword := os.Getenv("RUMP_AUTH_FROM"); sourcePassword != "" {
		fmt.Printf("Attempting AUTH for %s\n", *from)
		sourceOptions = redis.DialPassword(sourcePassword)
	}
	source, err := redis.DialURL(*from, sourceOptions)
	handle(err)
	fmt.Printf("Connection to %q was successful\n", *from)

	destOptions := redis.DialPassword("")
	if destPassword := os.Getenv("RUMP_AUTH_TO"); destPassword != "" {
		fmt.Printf("Attempting AUTH for %s\n", *to)
		destOptions = redis.DialPassword(destPassword)
	}
	destination, err := redis.DialURL(*to, destOptions)
	handle(err)
	fmt.Printf("Connection to %q was successful\n", *to)

	defer source.Close()
	defer destination.Close()

	// Channel where batches of keys will pass.
	queue := make(chan map[string]string, 100)

	// Scan and send to queue.
	go get(source, queue)

	// Restore keys as they come into queue.
	put(destination, queue)

	fmt.Println("Sync done.")
}
