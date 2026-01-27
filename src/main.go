package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

// DBInterface allows using either *sqlx.DB or *sqlx.Tx
type DBInterface interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Get(dest interface{}, query string, args ...interface{}) error
	Select(dest interface{}, query string, args ...interface{}) error
	QueryRow(query string, args ...interface{}) *sql.Row
}

type ActivitiesTexts struct {
	Activities []int
	Texts      []string
	Speaker    string
	Protocol   *Protocol
}

type ActivitiesTextsChan chan ActivitiesTexts

var activitiesTextsChan chan ActivitiesTexts = nil

var assignSpeechesToActivitiesWorkerRunning = false

var model ModelInterface = &GeminiModel{}

func getDateOrDefault(dateStr string, defaultTime time.Time) (time.Time, error) {
	if dateStr == "" {
		return defaultTime, nil
	}
	parsedDate, err := time.Parse(time.RFC3339, dateStr)
	return parsedDate, err
}

var START_DATE = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
var END_DATE = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

var logLevel LogStatus
var serviceLogPrefix = "Protocol Processing"

func buildDatabaseURL() (string, error) {
	requiredVars := map[string]string{
		"DATABASE_USER":     os.Getenv("DATABASE_USER"),
		"DATABASE_PASSWORD": os.Getenv("DATABASE_PASSWORD"),
		"DATABASE_HOST":     os.Getenv("DATABASE_HOST"),
		"DATABASE_PORT":     os.Getenv("DATABASE_PORT"),
		"DATABASE_NAME":     os.Getenv("DATABASE_NAME"),
	}

	var missingVars []string
	for varName, varValue := range requiredVars {
		if varValue == "" {
			missingVars = append(missingVars, varName)
		}
	}

	if len(missingVars) > 0 {
		return "", fmt.Errorf("missing required environment variables: %s", strings.Join(missingVars, ", "))
	}

	user := url.QueryEscape(requiredVars["DATABASE_USER"])
	password := url.QueryEscape(requiredVars["DATABASE_PASSWORD"])
	host := requiredVars["DATABASE_HOST"]
	port := requiredVars["DATABASE_PORT"]
	dbname := requiredVars["DATABASE_NAME"]

	databaseURL := fmt.Sprintf("postgres://%s:%s@%s:%s/%s", user, password, host, port, dbname)

	sslmode := os.Getenv("DATABASE_SSLMODE")
	if sslmode != "" {
		databaseURL += "?sslmode=" + url.QueryEscape(sslmode)
	}

	return databaseURL, nil
}

func main() {
	_ = godotenv.Load()

	databaseURL, err := buildDatabaseURL()
	if err != nil {
		log.Fatalf("Failed to build database URL: %v", err)
	}

	logLevel, err = GetLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		log.Fatalf("Failed to get log level: %v", err)
	}

	logLevel, err = GetLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		log.Fatalf("Failed to get log level: %v", err)
	}

	var db *sqlx.DB
	for true {
		db, err = sqlx.Connect("postgres", databaseURL)
		if err == nil {
			defer db.Close()
			break
		}
		log.Printf("Failed to connect to database: %v", err)
		time.Sleep(time.Second)
	}

	var mainLogger = NewLogger(db, &logLevel, &logLevel, serviceLogPrefix)
	err = model.Initialize(mainLogger)
	if err != nil {
		mainLogger.Fatal(fmt.Sprintf("Failed to initialize model: %v", err))
	}

	START_DATE, err = getDateOrDefault(os.Getenv("PROCESS_START_DATE"), START_DATE)
	if err != nil {
		mainLogger.Error(fmt.Sprintf("failed to parse PROCESS_START_DATE: %v", err))
		return
	}

	END_DATE, err = getDateOrDefault(os.Getenv("PROCESS_END_DATE"), END_DATE)
	if err != nil {
		mainLogger.Error(fmt.Sprintf("failed to parse PROCESS_END_DATE: %v", err))
		return
	}

	var assignSpeechesToActivitiesWorkerRunning = os.Getenv("BEGIN_PROCESSING_ON_STARTUP") == "true"

	activitiesTextsChan = make(chan ActivitiesTexts)

	workerCount, err := strconv.Atoi(os.Getenv("NUM_WORKERS"))
	if err != nil {
		mainLogger.Fatal(fmt.Sprintf("Invalid NUM_WORKERS value: %v", err))
	}

	for i := 0; i < workerCount; i++ {
		go func(workerId int) {
			count := 0
			workerPrefix := fmt.Sprintf("%s - Worker %d", serviceLogPrefix, workerId)
			fmt.Fprintf(os.Stdout, "Worker %d started\n", workerId)

			logger := NewLogger(db, &logLevel, &logLevel, workerPrefix)

			for {
				if !assignSpeechesToActivitiesWorkerRunning {
					time.Sleep(1 * time.Second)
					continue
				}
				count++
				shouldWait, err := processNextProtocol(logger)
				if err != nil {
					logger.Error(fmt.Sprintf("failed to assign speeches to activities: %v", err))
				}
				logger.SetPrefix(workerPrefix)
				if shouldWait {
					time.Sleep(1 * time.Minute)
					fmt.Fprintf(os.Stdout, "Worker %d sleeping for 1 minute\n", workerId)
				}
			}
		}(i)
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Server healthy")
	})

	http.HandleFunc("/control-processing", func(w http.ResponseWriter, r *http.Request) {
		assignSpeechesToActivitiesWorkerRunning = r.URL.Query().Get("start") == "true"
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Speeches assignment status updated with start="+strconv.FormatBool(assignSpeechesToActivitiesWorkerRunning))
	})

	http.HandleFunc("/process-single-protocol", func(w http.ResponseWriter, r *http.Request) {
		protocolId := r.URL.Query().Get("protocolId")
		protocolIdInt, err := strconv.Atoi(protocolId)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "Failed to parse protocolId", err)
			return
		}
		err = processSingleProtocol(protocolIdInt)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "Failed to process single protocol", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Single protocol processed successfully")
	})

	log.Println("Server starting on :8080")
	http.ListenAndServe(":8080", nil)
}
