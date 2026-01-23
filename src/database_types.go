package main

import (
	"database/sql"
	"time"
)

type DocumentType string

const (
	DocumentProtocol     DocumentType = "protocol"
	DocumentPrintedPaper DocumentType = "printedPaper"
)

type Body string

const (
	Bundestag         Body = "BT"
	Bundesrat         Body = "BR"
	Bundesversammlung Body = "BV"
	Europakammer      Body = "EK"
)

type ProcessingStatus string

const (
	ProcessingStatusNotStarted ProcessingStatus = "not_started"
	ProcessingStatusInProgress ProcessingStatus = "in_progress"
	ProcessingStatusCompleted  ProcessingStatus = "completed"
	ProcessingStatusFailed     ProcessingStatus = "failed"
)

type Protocol struct {
	ID                  int              `db:"id" json:"id,omitempty"`
	Title               string           `db:"title" json:"title,omitempty"`
	DocumentNumber      string           `db:"document_number" json:"document_number,omitempty"`
	Publisher           Body             `db:"publisher" json:"publisher,omitempty"`
	SessionNote         sql.NullString   `db:"session_note" json:"session_note,omitempty"`
	URL                 string           `db:"url" json:"url,omitempty"`
	Text                string           `db:"text" json:"text,omitempty"`
	ElectionPeriod      int              `db:"election_period" json:"election_period,omitempty"`
	Date                time.Time        `db:"date" json:"date,omitempty"`
	APIUpdated          time.Time        `db:"api_updated" json:"api_updated,omitempty"`
	Updated             time.Time        `db:"updated" json:"updated,omitempty"`
	Created             time.Time        `db:"created" json:"created,omitempty"`
	ProcessingStatus    ProcessingStatus `db:"processing_status" json:"processing_status,omitempty"`
	AttemptsCount       int              `db:"attempts_count" json:"attempts_count,omitempty"`
	ProcessingTimestamp sql.NullTime     `db:"processing_timestamp" json:"processing_timestamp,omitempty"`
}

type Activity struct {
	ID             int           `db:"id" json:"id,omitempty"`
	Type           string        `db:"type" json:"type,omitempty"`
	RoleID         int           `db:"role_id" json:"role_id,omitempty"`
	DocumentType   DocumentType  `db:"document_type" json:"document_type,omitempty"`
	PrintedPaperID sql.NullInt64 `db:"printed_paper_id" json:"printed_paper_id,omitempty"`
	ProtocolID     sql.NullInt64 `db:"protocol_id" json:"protocol_id,omitempty"`
	Text           string        `db:"text" json:"text,omitempty"`
	APIUpdated     time.Time     `db:"api_updated" json:"api_updated,omitempty"`
	Updated        time.Time     `db:"updated" json:"updated,omitempty"`
	Created        time.Time     `db:"created" json:"created,omitempty"`
}
