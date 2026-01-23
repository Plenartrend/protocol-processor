package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type PreviouslyUnfinishedSpeech struct {
	Present           bool
	Speaker           string
	SpeechStart       string
	BeginningTooShort bool
}

const ACCEPTABLE_UNASSIGNED_SPEECH_RATIO = 0.2

func resetActivities(protocolId int, db DBInterface, logger *Logger) error {
	_, err := db.Exec("UPDATE activities SET text = '' WHERE protocol_id = $1", protocolId)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to reset activities for protocol %d: %v", protocolId, err))
		return fmt.Errorf("failed to reset activities for protocol %d: %w", protocolId, err)
	}
	logger.Info(fmt.Sprintf("Successfully reset activities for protocol %d", protocolId))
	return nil
}

func addTextToActivity(protocolId int, speaker string, givenToProtocol bool, speech string, db DBInterface, logger *Logger) error {
	var activityId int
	var speechType string = "Rede"
	if givenToProtocol {
		speechType = "Rede (zu Protokoll gegeben)"
	}
	err := db.Get(&activityId, `
			SELECT a.id
			FROM activities a, roles r
			WHERE a.role_id = r.id
				AND a.protocol_id = $1
				AND a.type = $3
				AND (
					(r.name_suffix IS NOT NULL AND r.name_suffix != '' AND CONCAT(r.first_name, ' ', r.name_suffix, ' ', r.last_name) = $2)
					OR ((r.name_suffix IS NULL OR r.name_suffix = '') AND CONCAT(r.first_name, ' ', r.last_name) = $2)
				)
				AND (a.text IS NULL OR a.text = '')
			ORDER BY a.id asc
			LIMIT 1
		`, protocolId, speaker, speechType)

	if err == sql.ErrNoRows {
		logger.Warn(fmt.Sprintf("could not correlate speech by speaker '%s' in protocol %d to an activity", speaker, protocolId))
		return nil
	} else if err != nil {
		logger.Error(fmt.Sprintf(
			"[addTextToActivity] DB error during SELECT for activityId (protocolId=%d, speaker='%s', speechType='%s'): %v",
			protocolId, speaker, speechType, err,
		))
		return fmt.Errorf("failed to find activity for speaker %s in protocol %d: %w", speaker, protocolId, err)
	}

	logger.Debug(fmt.Sprintf("[addTextToActivity] Found activityId=%d for speaker='%s' in protocolId=%d and speechType='%s'", activityId, speaker, protocolId, speechType))

	cleanedSpeech := strings.ToValidUTF8(speech, "")
	if cleanedSpeech != speech {
		logger.Debug(fmt.Sprintf("[addTextToActivity] Speech text changed by ToValidUTF8 sanitization:\n%s\n", cleanedSpeech))
	}
	logger.Debug(fmt.Sprintf("[addTextToActivity] Updating activityId=%d with sanitized speech text (length=%d) and speechType='%s'",
		activityId, len(cleanedSpeech), speechType,
	))

	_, err = db.Exec("UPDATE activities SET text = $2 WHERE id = $1", activityId, cleanedSpeech)
	if err != nil {
		logger.Error(fmt.Sprintf(
			"[addTextToActivity] UPDATE failed for activityId=%d, speaker='%s' and speechType='%s': %v", activityId, speaker, speechType, err,
		))
		return fmt.Errorf("failed to update activity %d with speech for speaker %s and speechType '%s': %w", activityId, speaker, speechType, err)
	}

	logger.Debug(fmt.Sprintf("[addTextToActivity] Successfully updated activityId=%d with speech for speaker='%s' in protocolId=%d and speechType='%s'", activityId, speaker, protocolId, speechType))
	return nil
}

func processSpeeches(protocol *Protocol, chunkSize *int, db DBInterface, logger *Logger) error {
	err := resetActivities(protocol.ID, db, logger)
	if err != nil {
		return fmt.Errorf("failed to reset activities for protocol %d: %w", protocol.ID, err)
	}

	if chunkSize == nil {
		defaultChunkSize := 50_000
		chunkSize = &defaultChunkSize
	}
	var unmatchedSpeechesCount int = 0
	protocolText := protocol.Text
	protocolId := protocol.ID

	var previouslyUnfinishedSpeech PreviouslyUnfinishedSpeech = PreviouslyUnfinishedSpeech{
		Present:           false,
		Speaker:           "",
		SpeechStart:       "",
		BeginningTooShort: false,
	}

	startTime := time.Now()
	updatedActivities := 0

	for i := 0; i < len(protocolText); i += *chunkSize {

		end := min(i+*chunkSize, len(protocolText))
		chunk := protocolText[i:end]

		logger.Debug(fmt.Sprintf("Processing chunk from index %d to %d", i, end))

		var contextText string
		if previouslyUnfinishedSpeech.Present {
			contextText = "The previous chunk contained an unfinished speech by " + previouslyUnfinishedSpeech.Speaker + ". It started with: " + previouslyUnfinishedSpeech.SpeechStart
			if previouslyUnfinishedSpeech.BeginningTooShort == true {
				contextText += ". The previous chunk did not contain enough words of the new speech to provide an unambiguous start string. This chunk starts EXACTLY where the previous chunk ended. You must add a few words from the beginning of this chunk to speech_text_start until the total is 15-30 words. DO NOT ADD SPACES OR SIMILAR, APPEND IMMEDEATELY AT THE END OF speech_text_start."
			}
			contextText += "\n\n"
		} else {
			contextText = "The previous chunk did NOT contain an unfinished speech.\n\n"
		}

		if i > 0 {
			prevChunkStart := max(0, i-1000)
			endOfPreviousChunk := protocolText[prevChunkStart:i]
			contextText += "The end of the previous chunk is:\n" + endOfPreviousChunk + "\n\n"
		}

		query := []string{
			contextText,
			"The protocol chunk is:\n" + chunk,
		}

		response, err := model.GenerateContent(query, logger)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to generate content; retrying once: %v", err))
			response, err = model.GenerateContent(query, logger)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to generate content: %v", err))
				return fmt.Errorf("failed to generate content: %w", err)
			}
		}

		for _, speech := range response.CompleteSpeeches {
			if speech.Speaker == "" || speech.SpeechTextStart == "" || speech.SpeechTextEnd == "" {
				continue
			}

			// Reconstruct the full speech using start and end
			fullSpeech, err := getSpeechByStartAndEnd(speech.SpeechTextStart, speech.SpeechTextEnd, protocol, logger)
			if err != nil {
				logger.Warn(fmt.Sprintf("Skipping speech for speaker %s: %v", speech.Speaker, err))
				unmatchedSpeechesCount++
				if previouslyUnfinishedSpeech.Present && previouslyUnfinishedSpeech.Speaker == speech.Speaker {
					previouslyUnfinishedSpeech = PreviouslyUnfinishedSpeech{Present: false}
				}
				continue
			}

			logger.Info(fmt.Sprintf("Complete speech for Speaker %s (length=%d)", speech.Speaker, len(fullSpeech)))
			err = addTextToActivity(protocolId, speech.Speaker, speech.GivenToProtocol, fullSpeech, db, logger)
			if err != nil {
				return fmt.Errorf("failed to add completed speech to activity: %w", err)
			}
			updatedActivities++

			if previouslyUnfinishedSpeech.Present && previouslyUnfinishedSpeech.Speaker == speech.Speaker {
				previouslyUnfinishedSpeech = PreviouslyUnfinishedSpeech{Present: false}
			}
		}

		if response.StartedSpeech == nil {
			previouslyUnfinishedSpeech = PreviouslyUnfinishedSpeech{Present: false}
		} else {
			previouslyUnfinishedSpeech = PreviouslyUnfinishedSpeech{
				Present:           true,
				Speaker:           response.StartedSpeech.Speaker,
				SpeechStart:       response.StartedSpeech.SpeechTextStart,
				BeginningTooShort: response.StartedSpeech.BeginningTooShort,
			}

			logger.Info(fmt.Sprintf("Started/continuing unfinished speech for Speaker %s", response.StartedSpeech.Speaker))
		}

		logger.Info(fmt.Sprintf("Finished processing chunk from index %d to %d. Updated %d activities", i, end, updatedActivities))
	}
	logger.Info(fmt.Sprintf("Completed processing all chunks in %s", time.Since(startTime)))
	logger.Info(fmt.Sprintf("Total unmatched speeches (could not be matched at all): %d", unmatchedSpeechesCount))

	return nil
}

func processSingleProtocol(protocolId int) error {
	db, err := sqlx.Connect("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()
	loggerLevel := Debug
	logger := NewLogger(db, &loggerLevel, &loggerLevel)
	logger.AppendPrefix(fmt.Sprintf("protocol %d", protocolId))

	var protocol Protocol
	err = db.Get(&protocol, "SELECT * FROM protocols WHERE id = $1", protocolId)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to get protocol: %v", err))
		return err
	}

	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	err = processSpeeches(&protocol, nil, tx, logger)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to process speeches: %v", err))
		return fmt.Errorf("failed to process speeches: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	db.Exec("UPDATE protocols SET processing_status = 'completed' WHERE id = $1", protocolId)

	logger.Info(fmt.Sprintf("Successfully processed speeches for protocol %d", protocolId))
	return nil
}

func getUnassignedSpeechesCount(protocolId int, db DBInterface, logger *Logger) (float32, error) {
	var ratio float32
	err := db.Get(&ratio, `
		SELECT
			COUNT(*) FILTER (WHERE a.text = '')::float / COUNT(*)::float AS unassigned_rate
		FROM protocols p
			JOIN activities a ON a.protocol_id = p.id
		WHERE p.processing_status = 'completed' AND p.id = $1
			AND a.type like 'Rede%'
	`, protocolId)

	if err != nil {
		logger.Error(fmt.Sprintf("failed to get unassigned speeches count for protocol %d: %v", protocolId, err))
		return 0, fmt.Errorf("failed to get unassigned speeches count for protocol %d: %w", protocolId, err)
	}
	return ratio, nil
}

func processNextProtocol(logger *Logger) (bool, error) {
	db, err := sqlx.Connect("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		return true, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	if logger == nil {
		logger = NewLogger(db, nil, nil)
	}

	var protocols []Protocol
	query := `
		WITH to_update AS (
			SELECT p.id
			FROM protocols p
			WHERE
				(
					EXISTS (
						SELECT 1
						FROM activities a
						WHERE a.protocol_id = p.id AND (a.text IS NULL OR a.text = '') AND (a.type = 'Rede' OR a.type = 'Rede (zu Protokoll gegeben)')
					)
						AND p.processing_status = 'not_started'
					)
			OR (
				p.processing_status = 'failed'
					AND (
					p.attempts_count = 1
						OR (
						p.attempts_count = 2
							AND (now() - p.processing_timestamp > interval '1 day')
						)
					)
				)
			OR (
				p.processing_status = 'in_progress'
					AND (now() - p.processing_timestamp > interval '1 hour')
				)
			AND p.text IS NOT NULL AND p.text != '' AND p.text != '[NoTextAvailable]' AND length(p.text) > 1000
			AND p.date >= COALESCE($1, '1900-01-01') AND p.date <= COALESCE($2, '9999-12-31') -- NOTE: We currently NEVER reprocess protocols!
			ORDER BY p.date DESC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE protocols p
		SET processing_status = 'in_progress', 
			processing_timestamp = now(),
			attempts_count = attempts_count + 1
		FROM to_update t
		WHERE p.id = t.id
		RETURNING p.*;
		`
	handleError := func(protocolID int, err error, logger *Logger) {
		logger.Error(fmt.Sprintf("failed to process speeches for protocol %d: %v", protocolID, err))
		if err != nil {
			logger.Error(fmt.Sprintf("failed to update protocol %d: %v", protocolID, err))
		}
	}

	err = db.Select(&protocols, query, os.Getenv("PROCESS_START_DATE"), os.Getenv("PROCESS_END_DATE"))
	if err != nil {
		logger.Error(fmt.Sprintf("failed to select protocols: %v", err))
		return true, fmt.Errorf("failed to select protocols: %w", err)
	}

	//Len at most 1
	for _, protocol := range protocols {
		logger.AppendPrefix(fmt.Sprintf("protocol %d", protocol.ID))

		logger.Info(fmt.Sprintf("Processing speeches for protocol %d", protocol.ID))
		tx, err := db.Beginx()
		if err != nil {
			err = fmt.Errorf("failed to begin transaction: %w", err)
			handleError(protocol.ID, err, logger)
			continue
		}
		defer tx.Rollback()

		chunkSize := 50_000
		if protocol.AttemptsCount == 2 {
			chunkSize = 25_000 //Attempt smaller chunksSize
		} else if protocol.AttemptsCount == 3 {
			chunkSize = 75_000 //Attempt larger chunksSize
		}

		err = processSpeeches(&protocol, &chunkSize, tx, logger)
		if err != nil {
			err = fmt.Errorf("failed to process speeches: %w", err)
			handleError(protocol.ID, err, logger)
			continue
		}

		var count int
		err = db.Get(&count, "SELECT COUNT(*) FROM activities a WHERE a.protocol_id = $1 AND (a.type = 'Rede' OR a.type = 'Rede (zu Protokoll gegeben)') AND (a.text IS NULL OR a.text = '')", protocol.ID)
		if err != nil {
			err = fmt.Errorf("failed to check remaining speeches: %w", err)
			handleError(protocol.ID, err, logger)
			continue
		}
		if count > 0 {
			logger.Warn(fmt.Sprintf("There are still %d speeches without text for protocol %d", count, protocol.ID))
		} else {
			logger.Info(fmt.Sprintf("All speeches have been assigned for protocol %d", protocol.ID))
		}

		err = tx.Commit()
		if err != nil {
			err = fmt.Errorf("failed to commit transaction: %w", err)
			handleError(protocol.ID, err, logger)
			continue
		}

		unassignedRatio, err := getUnassignedSpeechesCount(protocol.ID, db, logger)
		if err != nil {
			err = fmt.Errorf("failed to get unassigned speeches count: %w", err)
			handleError(protocol.ID, err, logger)
			continue
		}

		var newProcessingStatus string = "completed"

		if (protocol.AttemptsCount < 3 && unassignedRatio > ACCEPTABLE_UNASSIGNED_SPEECH_RATIO) ||
			(protocol.AttemptsCount == 3 && unassignedRatio > 2*ACCEPTABLE_UNASSIGNED_SPEECH_RATIO) {
			logger.Warn(fmt.Sprintf("Unassigned speeches ratio %.2f exceeds acceptable limit for protocol %d (attempt %d)", unassignedRatio, protocol.ID, protocol.AttemptsCount))
			newProcessingStatus = "failed"
		} else {
			logger.Info(fmt.Sprintf("Successfully processed speeches for protocol %d", protocol.ID))
		}

		_, err = db.Exec("UPDATE protocols SET processing_status = $1 WHERE id = $2", newProcessingStatus, protocol.ID)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to set processed protocol %d to %s: %v", protocol.ID, newProcessingStatus, err))
			continue
		}
	}
	return len(protocols) == 0, nil
}
