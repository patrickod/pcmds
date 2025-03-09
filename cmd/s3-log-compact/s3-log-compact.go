package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	maxOutputSize    = 500 * 1024 * 1024 // 500MB in bytes
	workerCount      = 4                 // Number of concurrent download workers
	outputBufferSize = 1024 * 1024       // 1MB buffer for output files
)

type aggregator struct {
	client        *s3.Client
	sourceBucket  string
	sourcePrefix  string
	destBucket    string
	destPrefix    string
	currentBuffer *bytes.Buffer
	currentSize   int64
	fileCounter   int
	mu            sync.Mutex
}

func parseS3URI(uri string) (bucket, prefix string, err error) {
	if !strings.HasPrefix(uri, "s3://") {
		return "", "", fmt.Errorf("invalid S3 URI format: %s", uri)
	}
	parts := strings.SplitN(strings.TrimPrefix(uri, "s3://"), "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid S3 URI format: %s", uri)
	}
	return parts[0], parts[1], nil
}

func newAggregator(sourceBucket, sourcePrefix, destBucket, destPrefix string) (*aggregator, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg)
	return &aggregator{
		client:        client,
		sourceBucket:  sourceBucket,
		sourcePrefix:  sourcePrefix,
		destBucket:    destBucket,
		destPrefix:    destPrefix,
		currentBuffer: bytes.NewBuffer(make([]byte, 0, outputBufferSize)),
	}, nil
}

func humanizeBytes(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := unit, 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (a *aggregator) uploadBuffer(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.currentBuffer.Len() == 0 {
		return nil
	}

	a.fileCounter++
	key := fmt.Sprintf("%saggregated_%03d.gz", a.destPrefix, a.fileCounter)

	if dryRun {
		log.Printf("Dry run: would upload %s to %s/%s", humanizeBytes(a.currentBuffer.Len()), a.destBucket, key)
	} else {
		_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &a.destBucket,
			Key:    &key,
			Body:   bytes.NewReader(a.currentBuffer.Bytes()),
		})
		if err != nil {
			return fmt.Errorf("error uploading to S3: %w", err)
		}
	}

	a.currentBuffer.Reset()
	a.currentSize = 0
	return nil
}

func (a *aggregator) writeContent(ctx context.Context, content []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.currentSize+int64(len(content)) >= maxOutputSize {
		if err := a.uploadBuffer(ctx); err != nil {
			return err
		}
	}

	n, err := a.currentBuffer.Write(content)
	if err != nil {
		return fmt.Errorf("error writing to buffer: %w", err)
	}
	a.currentSize += int64(n)
	return nil
}

func (a *aggregator) processObject(ctx context.Context, key string) error {
	output, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &a.sourceBucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("error getting object %s: %w", key, err)
	}
	defer output.Body.Close()

	content, err := io.ReadAll(output.Body)
	if err != nil {
		return fmt.Errorf("error reading content from %s: %w", key, err)
	}

	return a.writeContent(ctx, content)
}

func (a *aggregator) run(ctx context.Context) error {
	// Create a channel for objects to process
	objChan := make(chan string)
	errChan := make(chan error, workerCount)
	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range objChan {
				if err := a.processObject(ctx, key); err != nil {
					errChan <- err
					return
				}
			}
		}()
	}

	// List and process objects
	paginator := s3.NewListObjectsV2Paginator(a.client, &s3.ListObjectsV2Input{
		Bucket: &a.sourceBucket,
		Prefix: &a.sourcePrefix,
	})

	processedCount := 0
	startTime := time.Now()

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			close(objChan)
			return fmt.Errorf("error listing objects: %w", err)
		}

		for _, obj := range page.Contents {
			select {
			case err := <-errChan:
				close(objChan)
				return err
			case objChan <- *obj.Key:
				processedCount++
				if processedCount%100 == 0 {
					elapsed := time.Since(startTime)
					rate := float64(processedCount) / elapsed.Seconds()
					log.Printf("Processed %d files (%.2f files/sec)", processedCount, rate)
				}
			}
		}
	}

	close(objChan)
	wg.Wait()

	// Upload any remaining content in the buffer
	if err := a.uploadBuffer(ctx); err != nil {
		return err
	}

	select {
	case err := <-errChan:
		return err
	default:
		log.Printf("Successfully processed %d files in %v", processedCount, time.Since(startTime))
		return nil
	}
}

var (
	dryRun bool
)

type LambdaInput struct {
	Date   string `json:"date"`
	Bucket string `json:"bucket"`
	DryRun bool   `json:"dryRun"`
}

func handleRequest(ctx context.Context, event LambdaInput) error {
	return processLogs(ctx, event.Date, event.Bucket, event.DryRun)
}

func processLogs(ctx context.Context, date, bucket string, dryRunFlag bool) error {
	if date == "" {
		return fmt.Errorf("date is required")
	}
	if bucket == "" {
		return fmt.Errorf("bucket is required")
	}

	dryRun = dryRunFlag

	sourceBucket, sourcePrefix, err := parseS3URI(fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		return err
	}

	destBucket, destPrefix, err := parseS3URI(fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		return err
	}

	agg, err := newAggregator(sourceBucket, sourcePrefix, destBucket, destPrefix)
	if err != nil {
		return err
	}

	// List all apps
	paginator := s3.NewListObjectsV2Paginator(agg.client, &s3.ListObjectsV2Input{
		Bucket:    &agg.sourceBucket,
		Prefix:    &agg.sourcePrefix,
		Delimiter: aws.String("/"),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("error listing apps: %w", err)
		}

		for _, prefix := range page.CommonPrefixes {
			appPrefix := *prefix.Prefix
			app := strings.TrimSuffix(strings.TrimPrefix(appPrefix, agg.sourcePrefix), "/")

			// Reset file counter for each app
			agg.fileCounter = 0

			// Process logs for the given date
			datePrefix := fmt.Sprintf("%s%s/", appPrefix, date)
			agg.sourcePrefix = datePrefix
			agg.destPrefix = fmt.Sprintf("%s%s/%s/", destPrefix, app, date) // Fix the broken path appending

			if err := agg.run(ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

func main() {
	runAsCLI := flag.Bool("cli", false, "Run as CLI utility")
	date := flag.String("date", "", "Date to process logs for (required for CLI)")
	bucket := flag.String("bucket", "", "S3 bucket to process logs from (required for CLI)")
	dryRunFlag := flag.Bool("dry-run", true, "Perform a dry run (CLI only)")
	flag.Parse()

	if *runAsCLI {
		if *date == "" || *bucket == "" {
			log.Fatal("Date and bucket are required for CLI mode")
		}

		if err := processLogs(context.Background(), *date, *bucket, *dryRunFlag); err != nil {
			log.Fatal(err)
		}
	} else {
		lambda.Start(handleRequest)
	}
}
