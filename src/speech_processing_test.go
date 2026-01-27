package main

import (
	"bufio"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/agnivade/levenshtein"
	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var PROTOCOL_ID = 5626
var PROTOCOL_TEXT_FILE = fmt.Sprintf("test_data/protocol_%d_text.txt", PROTOCOL_ID)
var SPEECH_TEST_FILE = fmt.Sprintf("test_data/protocol_%d_speeches.txt", PROTOCOL_ID)
var MAX_ACCEPTABLE_LEVENSHTEIN_RATIO = 0.1

func TestProcessSpeeches(t *testing.T) {
	err := godotenv.Load("../.env.test")
	if err != nil {
		t.Fatalf("Failed to load .env.test: %v", err)
	}

	databaseURL, err := buildDatabaseURL()
	if err != nil {
		t.Fatalf("Failed to build database URL: %v", err)
	}
	db, err := sqlx.Connect("postgres", databaseURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	tx, err := db.Beginx()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}
	defer tx.Rollback() // Rollback after test

	testLogger := NewLogger(db, nil, nil, "test-setup")

	err = model.Initialize(testLogger)
	if err != nil {
		t.Fatalf("Failed to initialize model: %v", err)
	}

	protocolText, err := os.ReadFile(PROTOCOL_TEXT_FILE)
	if err != nil {
		t.Fatalf("Failed to read protocol text file: %v", err)
	}

	t.Logf("Loaded protocol text: %d characters", len(protocolText))

	testProtocol := &Protocol{
		ID:   PROTOCOL_ID,
		Text: string(protocolText),
	}

	expectedSpeeches := loadExpectedSpeeches(t)
	t.Logf("Loaded %d expected speeches from test file", len(expectedSpeeches))

	_, err = tx.Exec(`UPDATE activities SET text = '' WHERE protocol_id = $1 AND type like 'Rede%'`, testProtocol.ID)
	if err != nil {
		t.Fatalf("Failed to clear existing activity text: %v", err)
	}
	t.Logf("Cleared existing speech text for protocol %d", testProtocol.ID)

	logger := NewLogger(db, nil, nil, "test-process-speeches")

	err = processSpeeches(testProtocol, nil, tx, logger)
	if err != nil {
		t.Errorf("processSpeeches failed: %v", err)
		return
	}

	var totalSpeechActivities int
	err = tx.Get(&totalSpeechActivities, `
		SELECT COUNT(*)
		FROM activities
		WHERE protocol_id = $1
			AND type like 'Rede%'
	`, testProtocol.ID)
	if err != nil {
		t.Fatalf("Failed to count total speech activities: %v", err)
	}

	var activities []struct {
		ID          int    `db:"id"`
		SpeakerName string `db:"speaker_name"`
		Text        string `db:"text"`
		Type        string `db:"type"`
	}
	err = tx.Select(&activities, `
		SELECT 
			a.id, 
			r.first_name || ' ' || COALESCE(r.name_suffix || ' ', '') || r.last_name as speaker_name,
			a.text,
			a.type
		FROM activities a, roles r
		WHERE r.id = a.role_id
			AND a.protocol_id = $1 
			AND a.type like 'Rede%'
			AND a.text IS NOT NULL AND a.text != ''
		ORDER BY a.id asc
	`, testProtocol.ID)
	if err != nil {
		t.Errorf("Failed to query activities: %v", err)
		return
	}

	t.Logf("==================== Test Statistics ====================")
	t.Logf("Total speech activities in DB: %d", totalSpeechActivities)
	var activitiesWithTextRatio float64 = float64(len(activities)) / float64(totalSpeechActivities) * 100
	t.Logf("Activities with assigned text: %d (%.1f%%)", len(activities), activitiesWithTextRatio)
	t.Logf("Activities without text: %d", totalSpeechActivities-len(activities))

	matchCount := 0
	mismatchCount := 0
	lowSimilarityCount := 0
	totalSimilarity := 0.0
	matchedExpectedSpeeches := make(map[string]bool)

	for _, activity := range activities {
		expectedText, exists := expectedSpeeches[activity.SpeakerName]

		if !exists {
			t.Logf("⚠ Activity %d (%s): no expected speech found in test file",
				activity.ID, activity.SpeakerName)
			continue
		}

		matchedExpectedSpeeches[activity.SpeakerName] = true

		if activity.Text == expectedText {
			matchCount++
			t.Logf("✓ Activity %d (%s): exact match (%d characters)",
				activity.ID, activity.SpeakerName, len(activity.Text))
		} else {
			mismatchCount++
			start := time.Now()
			levDistance := levenshtein.ComputeDistance(expectedText, activity.Text)
			t.Logf("Took %v for levenshtein computation", time.Since(start))
			totalSimilarity += float64(levDistance)
			lengthDiff := len(activity.Text) - len(expectedText)
			maxLen := max(len(expectedText), len(activity.Text))
			threshold := int(float64(maxLen) * MAX_ACCEPTABLE_LEVENSHTEIN_RATIO)
			distanceRatio := float64(levDistance) / float64(maxLen) * 100

			if levDistance > threshold {
				lowSimilarityCount++
				t.Errorf("✗ Activity %d (%s): mismatch - expected %d chars, got %d chars (diff: %+d), Levenshtein distance: %d (%.1f%%, threshold: %d)",
					activity.ID, activity.SpeakerName, len(expectedText), len(activity.Text), lengthDiff, levDistance, distanceRatio, threshold)
			} else {
				t.Logf("≈ Activity %d (%s): acceptable mismatch - expected %d chars, got %d chars (diff: %+d), Levenshtein distance: %d (%.1f%%)",
					activity.ID, activity.SpeakerName, len(expectedText), len(activity.Text), lengthDiff, levDistance, distanceRatio)
			}
		}
	}

	// Count unmatched expected speeches (speeches that couldn't be assigned to activities)
	unmatchedExpected := 0
	for speakerName := range expectedSpeeches {
		if !matchedExpectedSpeeches[speakerName] {
			unmatchedExpected++
			t.Logf("⚠ Expected speech for '%s' was not matched to any activity", speakerName)
		}
	}

	t.Logf("==================== Results Summary ====================")
	t.Logf("Expected speeches in test file: %d", len(expectedSpeeches))
	t.Logf("Exact matches: %d (%.1f%%)", matchCount, float64(matchCount)/float64(len(expectedSpeeches))*100)
	t.Logf("Mismatches: %d (%.1f%%)", mismatchCount, float64(mismatchCount)/float64(len(expectedSpeeches))*100)
	if mismatchCount > 0 {
		avgDistance := totalSimilarity / float64(mismatchCount)
		t.Logf("  - Average Levenshtein distance: %.1f", avgDistance)
	}
	t.Logf("  - Acceptable (<= %.0f%% of text length): %d", MAX_ACCEPTABLE_LEVENSHTEIN_RATIO*100, mismatchCount-lowSimilarityCount)
	t.Logf("  - High distance (> %.0f%%): %d (%.1f%%)", MAX_ACCEPTABLE_LEVENSHTEIN_RATIO*100, lowSimilarityCount, float64(lowSimilarityCount)/float64(len(expectedSpeeches))*100)
	t.Logf("Unmatched expected speeches: %d", unmatchedExpected)
	t.Logf("Activities with text but no expected speech: %d", len(activities)-matchCount-mismatchCount)

	if len(activities) == 0 {
		t.Error("No activities were updated with speech text")
		return
	}

	if lowSimilarityCount > 0 {
		t.Errorf("Found %d speeches with high Levenshtein distance (above %.0f%% ratio threshold)", lowSimilarityCount, MAX_ACCEPTABLE_LEVENSHTEIN_RATIO*100)
	}

	if matchCount == 0 {
		t.Error("No speeches matched expected values")
	}

	if unmatchedExpected > 0 {
		t.Errorf("%d expected speeches could not be assigned to activities (check logger warnings)", unmatchedExpected)
	}
}

func loadExpectedSpeeches(t *testing.T) map[string]string {
	file, err := os.Open(SPEECH_TEST_FILE)
	if err != nil {
		t.Fatalf("Failed to open speeches file: %v", err)
	}
	defer file.Close()

	speeches := make(map[string]string)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		speakerName := scanner.Text()

		if !scanner.Scan() {
			t.Fatalf("Expected speech line after speaker line: %s", speakerName)
		}

		speeches[speakerName] = scanner.Text()
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("Error reading speeches file: %v", err)
	}

	return speeches
}
