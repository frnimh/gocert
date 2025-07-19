package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"
)

const (
	// Default database path
	defaultDbPath = "/var/gocert/gocert.db"
	// Default base path for storing certificate files
	defaultCertsPath = "/var/gocert/certs"
	// Renew if the certificate has this many days or fewer remaining
	renewalThresholdRemainingDays = 10
	// Standard certificate validity in days
	certValidityDays = 90
	// How often the daemon checks certificates
	checkInterval = 1 * time.Hour
)

// Add a mutex for database write operations to ensure thread safety
var dbMutex = &sync.Mutex{}

// CertConfig defines the structure for each certificate entry in the YAML file.
type CertConfig struct {
	Type    string   `yaml:"type"`
	Issuer  string   `yaml:"issuer"`
	Domains []string `yaml:"domains"`
}

// CertDBRecord holds the full state of a certificate as stored in the database.
type CertDBRecord struct {
	Name       string
	Type       string
	Issuer     string
	Domains    string
	LastIssued time.Time
	Status     string
}

// setupDatabase initializes the SQLite database and creates/updates the certificates table.
func setupDatabase(dbPath string) (*sql.DB, error) {
	// Ensure the directory for the database exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	createStatement := `
	CREATE TABLE IF NOT EXISTS certificates (
		name TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		issuer TEXT NOT NULL,
		domains TEXT NOT NULL,
		last_issued TIMESTAMP,
		status TEXT NOT NULL DEFAULT 'unknown'
	);`

	if _, err = db.Exec(createStatement); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	alterStatement := `ALTER TABLE certificates ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown'`
	_, _ = db.Exec(alterStatement) // Ignore error if column already exists

	return db, nil
}

// getCertState retrieves the full state of a certificate from the database.
func getCertState(db *sql.DB, name string) (CertDBRecord, bool, error) {
	query := "SELECT name, type, issuer, domains, last_issued, status FROM certificates WHERE name = ?"
	row := db.QueryRow(query, name)

	var record CertDBRecord
	var lastIssued sql.NullTime

	err := row.Scan(&record.Name, &record.Type, &record.Issuer, &record.Domains, &lastIssued, &record.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return CertDBRecord{}, false, nil
		}
		return CertDBRecord{}, false, fmt.Errorf("failed to query certificate state for '%s': %w", name, err)
	}

	if lastIssued.Valid {
		record.LastIssued = lastIssued.Time
	}

	return record, true, nil
}

// updateCertState updates or inserts the full state of a certificate in the database.
func updateCertState(db *sql.DB, name string, config CertConfig, issueTime time.Time, status string) error {
	domainsStr := strings.Join(config.Domains, ",")
	var lastIssued sql.NullTime
	if !issueTime.IsZero() {
		lastIssued.Time = issueTime
		lastIssued.Valid = true
	}

	// Lock the mutex before performing a write operation to ensure thread safety.
	dbMutex.Lock()
	defer dbMutex.Unlock()

	query := `
	INSERT INTO certificates (name, type, issuer, domains, last_issued, status)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(name) DO UPDATE SET
		type=excluded.type,
		issuer=excluded.issuer,
		domains=excluded.domains,
		last_issued=excluded.last_issued,
		status=excluded.status;`

	_, err := db.Exec(query, name, config.Type, config.Issuer, domainsStr, lastIssued, status)
	if err != nil {
		return fmt.Errorf("failed to update certificate state for '%s': %w", name, err)
	}
	return nil
}

// issueCertificate runs the acme.sh command to issue or renew a certificate.
func issueCertificate(name string, config CertConfig, certsBasePath string) error {
	log.Printf("Issuing/Renewing certificate for '%s' with type '%s' and issuer '%s'\n", name, config.Type, config.Issuer)

	certDir := filepath.Join(certsBasePath, name)
	certFile := filepath.Join(certDir, "cert.pem")
	keyFile := filepath.Join(certDir, "key.pem")
	fullchainFile := filepath.Join(certDir, "fullchain.pem")

	if err := os.MkdirAll(certDir, 0755); err != nil {
		return fmt.Errorf("failed to create certificate directory for '%s': %w", name, err)
	}

	var domainArgs []string
	for _, domain := range config.Domains {
		domainArgs = append(domainArgs, "-d", domain)
	}
	log.Printf("Domains: %s\n", strings.Join(config.Domains, " "))

	args := []string{
		"--issue",
		"--dns", config.Type,
		"--cert-file", certFile,
		"--key-file", keyFile,
		"--fullchain-file", fullchainFile,
		"--server", config.Issuer,
		"--force",
	}
	args = append(args, domainArgs...)

	cmd := exec.Command("acme.sh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("acme.sh command failed for '%s': %w", name, err)
	}

	return nil
}

// processSingleCert checks and acts on a single certificate. It's designed to be run in a goroutine.
func processSingleCert(wg *sync.WaitGroup, name string, config CertConfig, db *sql.DB, certsBasePath string) {
	defer wg.Done()

	log.Printf("--- Checking certificate: %s ---", name)

	state, found, err := getCertState(db, name)
	if err != nil {
		log.Printf("Error getting state for '%s', skipping: %v", name, err)
		return
	}

	needsAction := false
	if !found {
		log.Printf("Certificate '%s' not found in database. Issuing for the first time.", name)
		needsAction = true
	} else {
		expiryDate := state.LastIssued.AddDate(0, 0, certValidityDays)
		remainingDuration := time.Until(expiryDate)
		remainingDays := int(remainingDuration.Hours() / 24)

		if remainingDays <= renewalThresholdRemainingDays {
			log.Printf("Certificate '%s' has %d days remaining. Renewing.", name, remainingDays)
			needsAction = true
		} else {
			log.Printf("Certificate '%s' is up to date (%d days remaining). No action needed.", name, remainingDays)
		}
	}

	if needsAction {
		err := issueCertificate(name, config, certsBasePath)
		var newStatus string
		var newIssueTime time.Time

		if err != nil {
			log.Printf("ERROR: Failed to issue certificate for '%s': %v", name, err)
			newStatus = "failed"
			newIssueTime = state.LastIssued // Keep old issue time on failure
		} else {
			log.Printf("Successfully issued/renewed certificate for '%s'", name)
			newStatus = "issued"
			newIssueTime = time.Now()
		}

		if err := updateCertState(db, name, config, newIssueTime, newStatus); err != nil {
			log.Printf("ERROR: Failed to update database for '%s': %v", name, err)
		}
	}
}

// checkAndProcessCertificates now launches a goroutine for each certificate.
func checkAndProcessCertificates(yamlFile string, db *sql.DB, certsBasePath string) {
	log.Println("Starting concurrent certificate check...")

	byteValue, err := os.ReadFile(yamlFile)
	if err != nil {
		log.Printf("ERROR: Failed to read YAML file '%s': %v", yamlFile, err)
		return
	}

	var certConfigs map[string]CertConfig
	err = yaml.Unmarshal(byteValue, &certConfigs)
	if err != nil {
		log.Printf("ERROR: Failed to parse YAML: %v", err)
		return
	}

	var wg sync.WaitGroup
	for name, config := range certConfigs {
		wg.Add(1)
		go processSingleCert(&wg, name, config, db, certsBasePath)
	}

	wg.Wait() // Wait for all certificate checks to complete.
	log.Printf("Certificate check finished. Next check in %s.", checkInterval)
}

// displayCertInfo shows the status of all managed certificates from the database.
func displayCertInfo(db *sql.DB) error {
	rows, err := db.Query("SELECT name, type, issuer, last_issued, status FROM certificates ORDER BY name")
	if err != nil {
		return fmt.Errorf("failed to query certificates: %w", err)
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tISSUED\tEXPIRES\tREMAINING\tTLS PROVIDER\tDNS PROVIDER")
	fmt.Fprintln(w, "----\t------\t------\t-------\t---------\t------------\t------------")

	var hasCerts bool
	for rows.Next() {
		hasCerts = true
		var record CertDBRecord
		var lastIssued sql.NullTime

		if err := rows.Scan(&record.Name, &record.Type, &record.Issuer, &lastIssued, &record.Status); err != nil {
			log.Printf("Warning: could not scan row: %v", err)
			continue
		}

		issuedStr, expiresStr, remainingStr := "N/A", "N/A", "N/A"

		if lastIssued.Valid {
			record.LastIssued = lastIssued.Time
			expiryDate := record.LastIssued.AddDate(0, 0, certValidityDays)
			remainingDuration := time.Until(expiryDate)
			remainingDays := int(remainingDuration.Hours() / 24)

			issuedStr = record.LastIssued.Format("2006-01-02")
			expiresStr = expiryDate.Format("2006-01-02")
			remainingStr = fmt.Sprintf("%d days", remainingDays)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			record.Name, record.Status, issuedStr, expiresStr, remainingStr, record.Issuer, record.Type)
	}

	if !hasCerts {
		fmt.Println("No certificates found in the database. Run with a config file first.")
		return nil
	}

	return w.Flush()
}

// printUsage displays the command-line usage instructions.
func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <command>\n\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintf(os.Stderr, "  info          Display the status of all managed certificates.\n")
	fmt.Fprintf(os.Stderr, "  run <file>    Run the certificate manager as a continuous daemon.\n")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	dbPath := os.Getenv("GOCERT_DB_PATH")
	if dbPath == "" {
		dbPath = defaultDbPath
	}
	certsPath := os.Getenv("GOCERT_CERTS_PATH")
	if certsPath == "" {
		certsPath = defaultCertsPath
	}

	db, err := setupDatabase(dbPath)
	if err != nil {
		log.Fatalf("Database setup failed: %v", err)
	}
	defer db.Close()

	command := os.Args[1]

	switch command {
	case "info":
		if err := displayCertInfo(db); err != nil {
			log.Fatalf("Failed to display certificate info: %v", err)
		}
	case "run":
		if len(os.Args) < 3 {
			log.Println("Error: 'run' command requires a file path.")
			printUsage()
			os.Exit(1)
		}
		yamlFile := os.Args[2]
		log.Printf("Starting certificate manager daemon...")
		log.Printf("Database path: %s", dbPath)
		log.Printf("Certs path: %s", certsPath)

		checkAndProcessCertificates(yamlFile, db, certsPath)

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			checkAndProcessCertificates(yamlFile, db, certsPath)
		}

	default:
		log.Printf("Error: Unknown command '%s'\n", command)
		printUsage()
		os.Exit(1)
	}
}
