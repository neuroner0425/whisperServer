package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"whisperserver/src/internal/config"
	"whisperserver/src/internal/integrations/storage"
	store "whisperserver/src/internal/repo/sqlite"
)

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func getFileSize(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return stat.Size()
}

func main() {
	var (
		confPath  = flag.String("conf", "", "Path to project root containing app.conf (defaults to current dir)")
		skipVac   = flag.Bool("skip-vacuum", false, "Skip running VACUUM after migration")
		dbPathArg = flag.String("db", "", "Path to SQLite database (defaults to .run/whisper.db)")
	)
	flag.Parse()

	root := *confPath
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("failed to get working dir: %v", err)
		}
		root = cwd
	}

	cfg, err := config.Load(root)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	dbPath := *dbPathArg
	if dbPath == "" {
		dbPath = filepath.Join(root, ".run", "whisper.db")
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("database file does not exist: %s", dbPath)
	}

	dbSizeBefore := getFileSize(dbPath)

	fmt.Println("================================================================================")
	fmt.Println("           WhisperServer Media BLOB Migration to Object Storage                 ")
	fmt.Println("================================================================================")
	fmt.Printf("Database Path     : %s (%s)\n", dbPath, formatBytes(dbSizeBefore))
	fmt.Printf("Storage Driver    : %s\n", cfg.StorageType)
	if cfg.StorageType == "s3" {
		fmt.Printf("S3 Endpoint       : %s (SSL: %v)\n", cfg.S3Endpoint, cfg.S3UseSSL)
		fmt.Printf("S3 Bucket         : %s\n", cfg.S3Bucket)
		fmt.Printf("S3 Region         : %s\n", cfg.S3Region)
		fmt.Printf("S3 Access Key ID  : %s\n", cfg.S3AccessKeyID)
	}
	fmt.Println("--------------------------------------------------------------------------------")

	// 1. Initialize Storage Driver
	var blobStore storage.Storage
	ctx := context.Background()

	if cfg.StorageType == "s3" {
		s3Cfg := storage.S3Config{
			Endpoint:        cfg.S3Endpoint,
			Region:          cfg.S3Region,
			Bucket:          cfg.S3Bucket,
			AccessKeyID:     cfg.S3AccessKeyID,
			SecretAccessKey: cfg.S3SecretAccessKey,
			UseSSL:          cfg.S3UseSSL,
		}
		s3Store, err := storage.NewS3Storage(ctx, s3Cfg)
		if err != nil {
			log.Fatalf("failed to connect to S3/MinIO: %v", err)
		}
		blobStore = s3Store
	} else {
		localDir := filepath.Join(root, ".run", "storage")
		localStore, err := storage.NewLocalStorage(localDir)
		if err != nil {
			log.Fatalf("failed to initialize local storage: %v", err)
		}
		blobStore = localStore
	}

	// 2. Open SQLite Database directly
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`PRAGMA busy_timeout = 30000;`); err != nil {
		log.Fatalf("failed to set busy timeout: %v", err)
	}

	// Check if job_blobs exists
	if !store.TableExists(db, "job_blobs") {
		fmt.Println("[INFO] job_blobs table does not exist or has already been migrated.")
		return
	}

	// Ensure job_media table exists
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS job_media (
			job_id          TEXT NOT NULL,
			kind            TEXT NOT NULL,
			storage_backend TEXT NOT NULL DEFAULT 's3',
			storage_key     TEXT NOT NULL,
			size_bytes      INTEGER NOT NULL DEFAULT 0,
			content_type    TEXT NOT NULL DEFAULT '',
			etag            TEXT NOT NULL DEFAULT '',
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_job_media_job_id ON job_media(job_id);
	`)
	if err != nil {
		log.Fatalf("failed to ensure job_media table: %v", err)
	}

	startTime := time.Now()
	backendName := cfg.StorageType

	onProgress := func(done, total int, jobID, kind string, nBytes int64) {
		pct := float64(done) / float64(total) * 100
		fmt.Printf("[%d/%d] (%5.1f%%) Migrated %s/%s (%s)\n", done, total, pct, jobID, kind, formatBytes(nBytes))
	}

	fmt.Println("Starting migration from job_blobs to object storage...")
	stats, err := store.MigrateJobBlobsToStorage(ctx, db, blobStore, backendName, !*skipVac, onProgress)
	if err != nil {
		log.Fatalf("\nMigration failed: %v", err)
	}

	// Close database before checking final file size so SQLite checkpoints and flushes
	_ = db.Close()

	duration := time.Since(startTime)
	dbSizeAfter := getFileSize(dbPath)
	savedBytes := dbSizeBefore - dbSizeAfter
	savedPct := 0.0
	if dbSizeBefore > 0 {
		savedPct = float64(savedBytes) / float64(dbSizeBefore) * 100
	}

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("Migration Summary:")
	fmt.Printf("  Total Blobs Found      : %d\n", stats.TotalBlobs)
	fmt.Printf("  Successfully Migrated  : %d\n", stats.MigratedBlobs)
	fmt.Printf("  Total Binary Payload   : %s\n", formatBytes(stats.MigratedBytes))
	fmt.Printf("  Legacy Table Dropped   : %v\n", stats.DroppedLegacy)
	fmt.Printf("  VACUUM Executed        : %v\n", stats.VacuumExecuted)
	fmt.Printf("  Database Size Before   : %s\n", formatBytes(dbSizeBefore))
	fmt.Printf("  Database Size After    : %s\n", formatBytes(dbSizeAfter))
	fmt.Printf("  Database Space Saved   : %s (%.1f%% reduction)\n", formatBytes(savedBytes), savedPct)
	fmt.Printf("  Total Elapsed Time     : %s\n", duration.Round(time.Millisecond))
	fmt.Println("================================================================================")
	fmt.Println("Migration completed successfully!")
}
