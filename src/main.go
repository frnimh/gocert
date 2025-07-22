package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
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
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

// Build-time variables, populated by ldflags
var (
	version = "dev"
	commit  = "none"
)

//go:embed schema.json
var schemaContent string

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
	// Full path to the acme.sh script inside the container
	acmeShPath = "/root/.acme.sh/acme.sh"
)

// Add a mutex for database write operations to ensure thread safety
var dbMutex = &sync.Mutex{}

// GlobalConfig holds top-level configuration like the account email.
type GlobalConfig struct {
	Email string `yaml:"email"`
}

// CertConfig defines the structure for each certificate entry in the YAML file.
type CertConfig struct {
	Type    string   `yaml:"type"`
	Issuer  string   `yaml:"issuer"`
	Domains []string `yaml:"domains"`
}

// FullConfig represents the entire structure of the YAML file,
// using an inline map to handle dynamic certificate names.
type FullConfig struct {
	Configs      GlobalConfig           `yaml:"configs"`
	Certificates map[string]CertConfig  `yaml:",inline"`
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

// validateConfig validates the YAML file content against the JSON schema
// that has been embedded into the binary.
func validateConfig(yamlContent []byte) error {
	// 1. Convert YAML to a generic interface{}
	var data interface{}
	if err := yaml.Unmarshal(yamlContent, &data); err != nil {
		return fmt.Errorf("failed to unmarshal YAML for validation: %w", err)
	}

	// 2. Convert the generic interface{} to JSON bytes
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to convert YAML to JSON for validation: %w", err)
	}

	// 3. Load schema from the embedded string variable
	schemaLoader := gojsonschema.NewStringLoader(schemaContent)
	documentLoader := gojsonschema.NewBytesLoader(jsonBytes)

	// 4. Perform validation
	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return fmt.Errorf("error during schema validation: %w", err)
	}

	if !result.Valid() {
		var errorMessages []string
		for _, desc := range result.Errors() {
			errorMessages = append(errorMessages, fmt.Sprintf("- %s", desc))
		}
		return fmt.Errorf("configuration validation failed:\n%s", strings.Join(errorMessages, "\n"))
	}

	log.Println("Configuration syntax is valid.")
	return nil
}


// setupDatabase initializes the SQLite database and creates/updates the certificates table.
func setupDatabase(dbPath string) (*sql.DB, error) {
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
	_, _ = db.Exec(alterStatement)

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

// registerAccount ensures the acme.sh account is registered with the provided email.
func registerAccount(email string) error {
	if email == "" {
		log.Println("Warning: No email found in config's 'configs' section. Account registration skipped.")
		return nil
	}

	log.Printf("Ensuring acme.sh account is registered with email: %s", email)
	cmd := exec.Command(acmeShPath, "--register-account", "-m", email)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		// This might not be a fatal error if the account already exists, but we'll log it.
		log.Printf("Warning: 'acme.sh --register-account' command finished with error, which might be okay if account already exists: %v", err)
	} else {
		log.Println("Account registration/update successful.")
	}
	// Return nil so the daemon doesn't stop for this non-critical warning.
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
		"--issue", "--dns", config.Type,
		"--cert-file", certFile, "--key-file", keyFile, "--fullchain-file", fullchainFile,
		"--server", config.Issuer, "--force",
	}
	args = append(args, domainArgs...)

	cmd := exec.Command(acmeShPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
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
			newIssueTime = state.LastIssued
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

// checkAndProcessCertificates is the core logic loop for the daemon.
func checkAndProcessCertificates(yamlFile string, db *sql.DB, certsBasePath string, isFirstRun bool) {
	log.Println("Starting certificate check...")

	byteValue, err := os.ReadFile(yamlFile)
	if err != nil {
		log.Printf("ERROR: Failed to read YAML file '%s': %v", yamlFile, err)
		return
	}

	// Validate the configuration before proceeding
	if err := validateConfig(byteValue); err != nil {
		log.Printf("ERROR: Invalid configuration in %s:\n%v", yamlFile, err)
		return // Stop processing if config is invalid
	}

	var fullConfig FullConfig
	if err := yaml.Unmarshal(byteValue, &fullConfig); err != nil {
		log.Printf("ERROR: Failed to parse YAML: %v", err)
		return
	}

	// On the first run of the daemon, register the account email.
	if isFirstRun {
		if err := registerAccount(fullConfig.Configs.Email); err != nil {
			// This is not a fatal error, so we just log it.
			log.Printf("Warning during account registration: %v", err)
		}
	}

	var wg sync.WaitGroup
	for name, config := range fullConfig.Certificates {
		wg.Add(1)
		go processSingleCert(&wg, name, config, db, certsBasePath)
	}

	wg.Wait()
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
	fmt.Fprintf(os.Stderr, "GoCert Manager: A daemon for automated TLS certificate management.\n\n")
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [arguments]\n\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintf(os.Stderr, "  run <file>    Run the certificate manager as a continuous daemon.\n")
	fmt.Fprintf(os.Stderr, "                <file>: Path to the YAML configuration file.\n\n")
	fmt.Fprintf(os.Stderr, "  status        Display the status of all managed certificates from the database.\n\n")
	fmt.Fprintf(os.Stderr, "  version       Display the build version and commit hash.\n\n")
	fmt.Fprintf(os.Stderr, "  help          Show this help message.\n")
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

	command := os.Args[1]

	// Commands that don't need a database connection
	switch command {
	case "version":
		fmt.Printf("gocert version: %s, commit: %s\n", version, commit)
		os.Exit(0)
	case "help":
		printUsage()
		os.Exit(0)
	}

	// Commands that need a database connection
	db, err := setupDatabase(dbPath)
	if err != nil {
		log.Fatalf("Database setup failed: %v", err)
	}
	defer db.Close()

	switch command {
	case "status":
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

		checkAndProcessCertificates(yamlFile, db, certsPath, true)

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			checkAndProcessCertificates(yamlFile, db, certsPath, false)
		}

	default:
		log.Printf("Error: Unknown command '%s'\n", command)
		printUsage()
		os.Exit(1)
	}
}
