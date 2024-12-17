package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	_ "modernc.org/sqlite"
)

type FFProbeResult struct {
	Streams []Stream `json:"streams"`
}

type Stream struct {
	CodecName     string `json:"codec_name"`
	CodecType     string `json:"codec_type"`
	SampleRate    string `json:"sample_rate"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	BitRate       string `json:"bit_rate"`
}

type ProbeResult struct {
	path      string
	probeData string
	err       error
}

func probeFile(path string) ProbeResult {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_streams",
		"-output_format", "json", path)
	output, err := cmd.Output()
	if err != nil {
		return ProbeResult{path: path, err: err}
	}

	var result FFProbeResult
	if err := json.Unmarshal(output, &result); err != nil {
		return ProbeResult{path: path, err: err}
	}

	if len(result.Streams) == 0 {
		return ProbeResult{path: path, err: err}
	}

	return ProbeResult{
		path:      path,
		probeData: string(output),
	}
}

func main() {
	inputDir := flag.String("dir", "", "Directory to scan for media files")
	dbPath := flag.String("db", "mediaaudit.db", "Output SQLite database path")
	workers := flag.Int("workers", runtime.NumCPU(), "Number of concurrent workers")
	flag.Parse()

	if *inputDir == "" {
		log.Fatal("Please specify an input directory with -dir")
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS media_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			filepath TEXT,
			probe_data TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatal(err)
	}

	fileChan := make(chan string)
	resultChan := make(chan ProbeResult)
	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range fileChan {
				resultChan <- probeFile(path)
			}
		}()
	}

	// Start DB writer goroutine
	go func() {
		for result := range resultChan {
			if result.err != nil {
				log.Printf("Error processing %s: %v", result.path, result.err)
				continue
			}

			_, err := db.Exec("INSERT INTO media_files (filepath, probe_data) VALUES (?, ?)",
				result.path, result.probeData)
			if err != nil {
				log.Printf("Error storing data for file %s: %v", result.path, err)
				continue
			}
			log.Printf("Processed: %s\n", result.path)
		}
	}()

	// Walk directory and send files to workers
	err = filepath.Walk(*inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			fileChan <- path
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	close(fileChan)
	wg.Wait()
	close(resultChan)
}
