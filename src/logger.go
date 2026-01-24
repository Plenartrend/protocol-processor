package main

import (
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
)

type LogStatus int

const (
	Debug LogStatus = iota
	Info
	Warn
	Error
	Fatal
)

type Logger struct {
	db               *sqlx.DB
	minConsoleLevel  LogStatus
	minDatabaseLevel LogStatus
	prefix           string
}

func NewLogger(db *sqlx.DB, minConsoleLevel *LogStatus, minDatabaseLevel *LogStatus, prefix ...string) *Logger {
	defaultLevel := Info
	if minConsoleLevel == nil {
		minConsoleLevel = &defaultLevel
	}
	if minDatabaseLevel == nil {
		minDatabaseLevel = &defaultLevel
	}
	loggerPrefix := ""
	if len(prefix) > 0 && prefix[0] != "" {
		loggerPrefix = prefix[0]
	}
	return &Logger{db: db, minConsoleLevel: *minConsoleLevel, minDatabaseLevel: *minDatabaseLevel, prefix: loggerPrefix}
}

func (l *Logger) SetPrefix(prefix string) {
	l.prefix = prefix
}

func (l *Logger) GetPrefix() string {
	return l.prefix
}

func (l *Logger) AppendPrefix(additionalPrefix string) {
	if l.prefix == "" {
		l.prefix = additionalPrefix
	} else {
		l.prefix = l.prefix + " - " + additionalPrefix
	}
}

func (l *Logger) formatMessage(message string) string {
	if l.prefix == "" {
		return message
	}
	return l.prefix + " - " + message
}

func (l *Logger) Debug(message string) {
	formattedMessage := l.formatMessage(message)
	if l.minConsoleLevel <= Debug {
		fmt.Println("DEBUG: ", formattedMessage)
	}
	if l.minDatabaseLevel <= Debug {
		l.Log("debug", formattedMessage)
	}
}

func (l *Logger) Info(message string) {
	formattedMessage := l.formatMessage(message)
	if l.minConsoleLevel <= Info {
		fmt.Println("INFO: ", formattedMessage)
	}
	if l.minDatabaseLevel <= Info {
		l.Log("info", formattedMessage)
	}
}

func (l *Logger) Warn(message string) {
	formattedMessage := l.formatMessage(message)
	if l.minConsoleLevel <= Warn {
		fmt.Println("WARN: ", formattedMessage)
	}
	if l.minDatabaseLevel <= Warn {
		l.Log("warn", formattedMessage)
	}
}

func (l *Logger) Error(message string) {
	formattedMessage := l.formatMessage(message)
	if l.minConsoleLevel <= Error {
		fmt.Println("ERROR: ", formattedMessage)
	}
	if l.minDatabaseLevel <= Error {
		l.Log("error", formattedMessage)
	}
}

func (l *Logger) Fatal(message string) {
	formattedMessage := l.formatMessage(message)
	if l.minDatabaseLevel <= Fatal {
		l.Log("fatal", formattedMessage)
	}
	if l.minConsoleLevel <= Fatal {
		fmt.Println("FATAL: ", formattedMessage)
	}
	panic(message)
}

func (l *Logger) Log(status string, message string) {
	_, err := l.db.Exec("INSERT INTO logs (timestamp, status, message) VALUES (NOW(), $1, $2)", status, message)
	if err != nil {
		log.Printf("ERROR: Failed to write log to database: %v", err)
	}
}
