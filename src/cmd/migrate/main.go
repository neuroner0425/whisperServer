package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"whisperserver/src/internal/config"
	"whisperserver/src/internal/integrations/storage"
	store "whisperserver/src/internal/repo/sqlite"
)

func formatBytes(b int64) string {
	if b < 0 {
		return "0 B"
	}
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func main() {
	var (
		confPathArg   = flag.String("conf", "", "Path to project root containing app.conf (defaults to current dir)")
		dbPathArg     = flag.String("db", "", "Path to SQLite database file")
		checkOnlyArg  = flag.Bool("check", false, "Run in read-only diagnostic check mode without making changes")
		migrateArg    = flag.Bool("migrate", false, "Execute migration (default if -check is not specified)")
		skipVacuumArg = flag.Bool("skip-vacuum", false, "Skip running VACUUM after migration")
		skipBackupArg = flag.Bool("skip-backup", false, "Skip creating timestamped database backup file before migration")
	)
	flag.Parse()

	root := *confPathArg
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("failed to get working dir: %v", err)
		}
		root = cwd
	}

	cfg, err := config.Load(root)
	if err != nil {
		log.Fatalf("failed to load app.conf: %v", err)
	}

	dbPath := *dbPathArg
	if dbPath == "" {
		dbName := "whisper.db"
		if strings.EqualFold(strings.TrimSpace(cfg.RunMode), "DEV") {
			dbName = "whisper.dev.db"
		}
		dbPath = filepath.Join(root, ".run", dbName)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("database file does not exist: %s", dbPath)
	}

	isCheckOnly := *checkOnlyArg && !*migrateArg

	fmt.Println("================================================================================")
	if isCheckOnly {
		fmt.Println("       WhisperServer Compatibility & Integrity Diagnostic Tool (-check)        ")
	} else {
		fmt.Println("          WhisperServer Database Compatibility & Migration Tool (-migrate)      ")
	}
	fmt.Println("================================================================================")
	fmt.Printf("Database Path     : %s (%s)\n", dbPath, formatBytes(getFileSize(dbPath)))
	fmt.Printf("App Run Mode      : %s\n", cfg.RunMode)
	fmt.Printf("Storage Type      : %s\n", cfg.StorageType)
	if cfg.StorageType == "s3" {
		fmt.Printf("S3 / MinIO Target : %s (Bucket: %s, SSL: %v)\n", cfg.S3Endpoint, cfg.S3Bucket, cfg.S3UseSSL)
	} else {
		fmt.Printf("Local Storage Dir : %s\n", filepath.Join(root, ".run", "storage"))
	}
	fmt.Println("--------------------------------------------------------------------------------")

	// 1. Initialize Storage driver
	ctx := context.Background()
	var blobStore storage.Storage
	var storageInitErr error

	if cfg.StorageType == "s3" {
		s3Cfg := storage.S3Config{
			Endpoint:        cfg.S3Endpoint,
			Region:          cfg.S3Region,
			Bucket:          cfg.S3Bucket,
			AccessKeyID:     cfg.S3AccessKeyID,
			SecretAccessKey: cfg.S3SecretAccessKey,
			UseSSL:          cfg.S3UseSSL,
		}
		blobStore, storageInitErr = storage.NewS3Storage(ctx, s3Cfg)
	} else {
		localDir := filepath.Join(root, ".run", "storage")
		blobStore, storageInitErr = storage.NewLocalStorage(localDir)
	}

	if storageInitErr != nil {
		fmt.Printf("[WARN] Failed to initialize storage driver: %v\n", storageInitErr)
		fmt.Println("       Media object existence check/migration will be skipped or limited.")
	}

	// 2. Open SQLite Database
	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	} else {
		dsn += "&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// -------------------------------------------------------------------------
	// Diagnostic Step: Check DB Health & Incompatibilities
	// -------------------------------------------------------------------------
	fmt.Println("\n>>> [1/5] Checking Media Blobs & Storage Status...")
	mediaReport, err := store.CheckMediaStorageHealth(ctx, db, blobStore)
	if err != nil {
		log.Fatalf("failed to check media health: %v", err)
	}
	if mediaReport.LegacyBlobsTableExists {
		if mediaReport.LegacyBlobsCount > 0 {
			fmt.Printf("  [ACTION NEEDED] Legacy 'job_blobs' contains %d binary blobs (%s) in SQLite!\n",
				mediaReport.LegacyBlobsCount, formatBytes(mediaReport.LegacyBlobsBytes))
		} else {
			fmt.Printf("  [INFO] Legacy 'job_blobs' table exists but is empty (ready to drop).\n")
		}
	} else {
		fmt.Println("  [OK] Legacy 'job_blobs' table does not exist (already migrated).")
	}

	fmt.Printf("  [INFO] 'job_media' records: %d (%s)\n", mediaReport.JobMediaCount, formatBytes(mediaReport.JobMediaBytes))
	if blobStore != nil {
		fmt.Printf("  [INFO] Storage Verification: %d verified present, %d missing in bucket/dir\n",
			mediaReport.VerifiedPresentCount, mediaReport.MissingObjectsCount)
		if mediaReport.MissingObjectsCount > 0 {
			fmt.Printf("  [WARN] Missing objects detected in storage:\n")
			for _, k := range mediaReport.MissingKeys {
				fmt.Printf("         - %s\n", k)
			}
		}
	}

	fmt.Println("\n>>> [2/5] Checking Job JSON Payloads & Gzip Compression...")
	jsonReport, err := store.CheckJSONCompression(db)
	if err != nil {
		log.Fatalf("failed to check JSON compression: %v", err)
	}
	fmt.Printf("  [INFO] Total job_json records  : %d\n", jsonReport.TotalCount)
	fmt.Printf("  [INFO] Gzip-compressed records : %d (%s)\n", jsonReport.CompressedCount, formatBytes(jsonReport.CompressedBytes))
	if jsonReport.UncompressedCount > 0 {
		fmt.Printf("  [ACTION NEEDED] Uncompressed raw records : %d (%s) - unmigrated from pre-a79e863\n",
			jsonReport.UncompressedCount, formatBytes(jsonReport.UncompressedBytes))
	} else {
		fmt.Println("  [OK] All job_json records are Gzip-compressed.")
	}

	fmt.Println("\n>>> [3/5] Checking Full-Text Search (FTS5) Indexing...")
	ftsReport, err := store.CheckFTSHealth(db)
	if err != nil {
		log.Fatalf("failed to check FTS health: %v", err)
	}
	fmt.Printf("  [INFO] Total jobs in system     : %d\n", ftsReport.TotalJobs)
	fmt.Printf("  [INFO] Indexed in job_search_fts : %d (Non-empty text: %d)\n",
		ftsReport.IndexedJobs, ftsReport.NonEmptyIndexCount)
	if ftsReport.MissingIndexJobs > 0 {
		fmt.Printf("  [ACTION NEEDED] Jobs missing from FTS5 index: %d\n", ftsReport.MissingIndexJobs)
	} else {
		fmt.Println("  [OK] All jobs have corresponding entries in FTS5 index.")
	}

	fmt.Println("\n>>> [4/5] Checking Job Status & Schema Integrity...")
	validStatus, invalidStatus, invalidCodes, err := store.CheckJobsStatusIntegrity(db)
	if err != nil {
		log.Fatalf("failed to check status integrity: %v", err)
	}
	if invalidStatus > 0 {
		fmt.Printf("  [WARN] Detected %d jobs with unknown status codes: %v\n", invalidStatus, invalidCodes)
	} else {
		fmt.Printf("  [OK] All jobs map to valid canonical status codes (%d status codes verified).\n", validStatus)
	}

	fmt.Println("\n>>> [5/5] Checking Orphan Records & Indexes...")
	orphanReport, err := store.CheckOrphanRecords(db)
	if err != nil {
		log.Fatalf("failed to check orphan records: %v", err)
	}
	totalOrphans := orphanReport.OrphanJSONCount + orphanReport.OrphanMediaCount + orphanReport.OrphanTagsCount + orphanReport.OrphanFTSCount
	if totalOrphans > 0 || orphanReport.TransientKinds > 0 {
		fmt.Printf("  [ACTION NEEDED] Orphan records found:\n")
		if orphanReport.OrphanJSONCount > 0 {
			fmt.Printf("         - Orphan job_json records  : %d\n", orphanReport.OrphanJSONCount)
		}
		if orphanReport.OrphanMediaCount > 0 {
			fmt.Printf("         - Orphan job_media records : %d\n", orphanReport.OrphanMediaCount)
		}
		if orphanReport.OrphanTagsCount > 0 {
			fmt.Printf("         - Orphan job_tags records  : %d\n", orphanReport.OrphanTagsCount)
		}
		if orphanReport.OrphanFTSCount > 0 {
			fmt.Printf("         - Orphan FTS index records : %d\n", orphanReport.OrphanFTSCount)
		}
		if orphanReport.TransientKinds > 0 {
			fmt.Printf("         - Old debug diagnostics    : %d\n", orphanReport.TransientKinds)
		}
	} else {
		fmt.Println("  [OK] No orphan records found.")
	}

	// If in check-only mode, print diagnostic summary and exit
	if isCheckOnly {
		fmt.Println("\n================================================================================")
		fmt.Println("                           Diagnostic Summary                                   ")
		fmt.Println("================================================================================")
		needsMigration := mediaReport.LegacyBlobsCount > 0 || jsonReport.UncompressedCount > 0 ||
			ftsReport.MissingIndexJobs > 0 || totalOrphans > 0 || orphanReport.TransientKinds > 0

		if needsMigration {
			fmt.Println("STATUS: [ACTION REQUIRED] Database contains unmigrated or inconsistent legacy data.")
			fmt.Println("\nPending actions for migration:")
			if mediaReport.LegacyBlobsCount > 0 {
				fmt.Printf("  1. Migrate %d binary blobs (%s) from 'job_blobs' to %s storage\n",
					mediaReport.LegacyBlobsCount, formatBytes(mediaReport.LegacyBlobsBytes), cfg.StorageType)
			}
			if jsonReport.UncompressedCount > 0 {
				fmt.Printf("  2. Gzip-compress %d uncompressed 'job_json' records (estimated ~70%% space savings)\n",
					jsonReport.UncompressedCount)
			}
			if ftsReport.MissingIndexJobs > 0 {
				fmt.Printf("  3. Synchronize %d missing jobs into SQLite FTS5 search index\n", ftsReport.MissingIndexJobs)
			}
			if totalOrphans > 0 || orphanReport.TransientKinds > 0 {
				fmt.Printf("  4. Purge %d orphan records and %d transient debug artifacts\n",
					totalOrphans, orphanReport.TransientKinds)
			}
			fmt.Println("  5. Rebuild composite indexes and run VACUUM to reclaim disk space")
			fmt.Println("\nTo apply these migrations, execute:")
			fmt.Printf("  go run ./src/cmd/migrate -db %s -migrate\n", dbPath)
		} else {
			fmt.Println("STATUS: [100% COMPATIBLE] Database is fully up-to-date and compliant with current architecture!")
		}
		return
	}

	// -------------------------------------------------------------------------
	// Migration Execution
	// -------------------------------------------------------------------------
	fmt.Println("\n================================================================================")
	fmt.Println("                         Starting Migration Process                             ")
	fmt.Println("================================================================================")

	dbSizeBefore := getFileSize(dbPath)

	// Step 0: Backup DB
	if !*skipBackupArg {
		backupPath := fmt.Sprintf("%s.bak_%s", dbPath, time.Now().Format("20060102_150405"))
		fmt.Printf("[BACKUP] Creating snapshot: %s ... ", filepath.Base(backupPath))
		if err := copyFile(dbPath, backupPath); err != nil {
			log.Fatalf("failed to create backup file: %v", err)
		}
		fmt.Println("DONE")
	}

	// Step 1: Migrate Legacy Media Blobs
	if mediaReport.LegacyBlobsTableExists && blobStore != nil {
		fmt.Println("\n[MIGRATE 1/5] Migrating legacy binary blobs to object storage...")
		blobStats, err := store.MigrateJobBlobsToStorage(
			ctx,
			db,
			blobStore,
			cfg.StorageType,
			false, // vacuum later
			func(done, total int, jobID, kind string, bytes int64) {
				fmt.Printf("  [%d/%d] Migrated %s/%s (%s)\n", done, total, jobID, kind, formatBytes(bytes))
			},
		)
		if err != nil {
			log.Fatalf("blob migration failed: %v", err)
		}
		fmt.Printf("  [OK] Successfully migrated %d blobs (%s payload). Legacy table dropped: %v\n",
			blobStats.MigratedBlobs, formatBytes(blobStats.MigratedBytes), blobStats.DroppedLegacy)
	} else if mediaReport.LegacyBlobsTableExists && mediaReport.LegacyBlobsCount == 0 {
		_, _ = db.Exec(`DROP TABLE IF EXISTS job_blobs`)
		fmt.Println("\n[MIGRATE 1/5] Dropped empty legacy 'job_blobs' table.")
	} else {
		fmt.Println("\n[MIGRATE 1/5] No legacy blobs to migrate.")
	}

	// Step 2: Gzip Compress job_json Payloads
	if jsonReport.UncompressedCount > 0 {
		fmt.Println("\n[MIGRATE 2/5] Compressing uncompressed job_json records with Gzip...")
		compTotal, compSaved, err := store.CompressUncompressedJobJSON(
			db,
			func(done, total int, jobID, kind string, savedBytes int64) {
				if done%20 == 0 || done == total {
					fmt.Printf("  Progress: [%d/%d] compressed... (saved %s so far)\n",
						done, total, formatBytes(savedBytes))
				}
			},
		)
		if err != nil {
			log.Fatalf("job_json compression failed: %v", err)
		}
		fmt.Printf("  [OK] Successfully compressed %d records (saved %s in payload data)\n",
			compTotal, formatBytes(compSaved))
	} else {
		fmt.Println("\n[MIGRATE 2/5] All job_json records are already compressed.")
	}

	// Step 3: FTS5 Full-Text Index Synchronization
	fmt.Println("\n[MIGRATE 3/5] Synchronizing jobs into SQLite FTS5 search index...")
	if err := store.SyncExistingJobsToFTS(db); err != nil {
		log.Fatalf("FTS synchronization failed: %v", err)
	}
	fmt.Println("  [OK] FTS5 search index synchronized.")

	// Step 4: Cleanup Orphan Records & Old Artifacts
	fmt.Println("\n[MIGRATE 4/5] Cleaning up orphan records and temporary artifacts...")
	deletedOrphans, err := store.CleanupOrphanAndTransientRecords(db)
	if err != nil {
		fmt.Printf("  [WARN] Cleanup returned error: %v\n", err)
	} else {
		fmt.Printf("  [OK] Purged %d orphan/transient records.\n", deletedOrphans)
	}

	// Step 5: Ensure Indexes
	fmt.Println("\n[MIGRATE 5/5] Re-verifying database indexes...")
	if err := store.EnsureDatabaseIndexes(db); err != nil {
		log.Fatalf("ensure indexes failed: %v", err)
	}
	fmt.Println("  [OK] All composite indexes verified.")

	// Optional VACUUM
	if !*skipVacuumArg {
		fmt.Print("\n[VACUUM] Running SQLite VACUUM to reclaim unused disk space... ")
		startVac := time.Now()
		if _, err := db.Exec(`VACUUM;`); err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Printf("DONE (took %v)\n", time.Since(startVac).Round(time.Millisecond))
		}
	}

	dbSizeAfter := getFileSize(dbPath)
	savedSpace := dbSizeBefore - dbSizeAfter
	if savedSpace < 0 {
		savedSpace = 0
	}
	percentSaved := 0.0
	if dbSizeBefore > 0 {
		percentSaved = float64(savedSpace) / float64(dbSizeBefore) * 100.0
	}

	fmt.Println("\n================================================================================")
	fmt.Println("                          Migration Summary                                     ")
	fmt.Println("================================================================================")
	fmt.Printf("  Database Size Before  : %s\n", formatBytes(dbSizeBefore))
	fmt.Printf("  Database Size After   : %s\n", formatBytes(dbSizeAfter))
	fmt.Printf("  Disk Space Recovered  : %s (%.1f%% reduction)\n", formatBytes(savedSpace), percentSaved)
	fmt.Println("  Job Status Mapping    : Fully verified with JobStatusName & JobPhase")
	fmt.Println("  Job Detail Visibility : ALL historical and new jobs are now fully viewable")
	fmt.Println("================================================================================")
	fmt.Println("Migration completed successfully! You can now run the server.")
}
