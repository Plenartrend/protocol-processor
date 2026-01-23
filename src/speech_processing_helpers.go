package main

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/agnivade/levenshtein"
)

// createFlexibleWhitespacePattern converts a search string into a regex pattern
// where each space can match various combinations of spaces and newlines
func createFlexibleWhitespacePattern(searchString string) *regexp.Regexp {
	// Escape special regex characters in the search string
	escaped := regexp.QuoteMeta(searchString)
	// Replace spaces with flexible whitespace pattern that matches one or more space/newline characters
	// This matches: " ", "\n", " \n", "\n ", " \n ", etc.
	pattern := strings.ReplaceAll(escaped, ` `, `[ \n]+`)
	return regexp.MustCompile(pattern)
}

func findExactMatches(pattern *regexp.Regexp, haystack string, logger *Logger) []int {
	var positions []int
	matches := pattern.FindAllStringIndex(haystack, -1)
	for _, match := range matches {
		positions = append(positions, match[0])
	}
	return positions
}

// tryRemoveFromEnd tries to find matches by progressively removing words from the end
func tryRemoveFromEnd(words []string, haystack string, logger *Logger) ([]int, int) {
	for numWords := len(words); numWords >= 3; numWords-- {
		searchString := strings.Join(words[:numWords], " ")
		pattern := createFlexibleWhitespacePattern(searchString)

		matchPositions := findExactMatches(pattern, haystack, logger)

		if len(matchPositions) > 0 {
			logger.Debug(fmt.Sprintf("findBestMatch: found %d matches with %d words (removed from end)", len(matchPositions), numWords))
			return matchPositions, numWords
		}
	}
	return nil, 0
}

// tryRemoveFromStart tries to find matches by progressively removing words from the start
func tryRemoveFromStart(words []string, haystack string, logger *Logger) ([]int, int, int, int) {
	for numWordsToRemove := 1; numWordsToRemove <= len(words)-3; numWordsToRemove++ {
		searchString := strings.Join(words[numWordsToRemove:], " ")
		removedPrefix := strings.Join(words[:numWordsToRemove], " ") + " "
		pattern := createFlexibleWhitespacePattern(searchString)

		matchPositions := findExactMatches(pattern, haystack, logger)

		if len(matchPositions) > 0 {
			numWordsUsed := len(words) - numWordsToRemove
			removedPrefixLen := len(removedPrefix)
			logger.Debug(fmt.Sprintf("findBestMatch: found %d matches with %d words (removed %d from front)", len(matchPositions), numWordsUsed, numWordsToRemove))
			return matchPositions, numWordsUsed, numWordsToRemove, removedPrefixLen
		}
	}
	return nil, 0, 0, 0
}

// tryMiddle tries to find matches using 3 words from the middle
func tryMiddle(words []string, haystack string, logger *Logger) ([]int, int, int, int) {
	if len(words) < 5 {
		return nil, 0, 0, 0
	}

	middleStart := (len(words) - 3) / 2
	middleEnd := middleStart + 3
	searchString := strings.Join(words[middleStart:middleEnd], " ")
	pattern := createFlexibleWhitespacePattern(searchString)

	var removedPrefix string
	if middleStart > 0 {
		removedPrefix = strings.Join(words[:middleStart], " ") + " "
	}

	matchPositions := findExactMatches(pattern, haystack, logger)

	if len(matchPositions) > 0 {
		removedPrefixLen := len(removedPrefix)
		logger.Debug(fmt.Sprintf("findBestMatch: found %d matches with 3 middle words (offset by %d words from front)", len(matchPositions), middleStart))
		return matchPositions, 3, middleStart, removedPrefixLen
	}
	return nil, 0, 0, 0
}

// findBestMatchDirection controls which direction to try first when removing words
type findBestMatchDirection int

const (
	removeFromEndFirst findBestMatchDirection = iota
	removeFromStartFirst
)

func findBestMatch(needle string, haystack string, maxDistanceRatio float64, direction findBestMatchDirection, logger *Logger) (startIdx int, found bool) {
	startTime := time.Now()
	needleLen := len(needle)
	haystackLen := len(haystack)

	logger.Debug(fmt.Sprintf("findBestMatch called: needleLen=%d, haystackLen=%d, maxDistanceRatio=%.2f, direction=%v", needleLen, haystackLen, maxDistanceRatio, direction))

	if needleLen == 0 || needleLen > haystackLen {
		logger.Warn(fmt.Sprintf("findBestMatch: invalid input (needleLen=%d, haystackLen=%d). Needle:\n%s", needleLen, haystackLen, needle))
		return -1, false
	}

	maxDistance := int(float64(needleLen) * maxDistanceRatio)
	words := strings.Fields(needle)

	// Helper function to evaluate candidates and return best match
	evaluateCandidates := func(matchPositions []int, numWordsUsed int, wordsRemovedFromFront int, removedPrefixLen int) (int, int, bool) {
		bestDistance := math.MaxInt
		bestIdx := -1

		for _, matchPos := range matchPositions {
			// If we removed words from the front, adjust the position back
			adjustedPos := matchPos
			if wordsRemovedFromFront > 0 {
				// Go back by the length of the removed prefix
				adjustedPos = matchPos - removedPrefixLen
				if adjustedPos < 0 {
					continue // Can't go back that far
				}
			}

			// Extract window of original needle length starting at adjusted position
			if adjustedPos+needleLen > haystackLen {
				continue // Not enough text left
			}

			window := haystack[adjustedPos : adjustedPos+needleLen]
			//logger.Debug(fmt.Sprintf("findBestMatch: needle: %s\nwindow: %s", needle, window))
			distance := levenshtein.ComputeDistance(needle, window)

			if distance < bestDistance {
				bestDistance = distance
				bestIdx = adjustedPos
			}

			// Early exit on exact match
			if distance == 0 {
				return bestIdx, bestDistance, true
			}
		}

		return bestIdx, bestDistance, false
	}

	var bestIdx = -1
	var bestDistance = math.MaxInt
	var matchPositions []int
	var numWordsUsed int
	var wordsRemovedFromFront int
	var removedPrefixLen int

	// Try the preferred direction first
	if direction == removeFromEndFirst {
		matchPositions, numWordsUsed = tryRemoveFromEnd(words, haystack, logger)
		if len(matchPositions) > 0 {
			idx, dist, exact := evaluateCandidates(matchPositions, numWordsUsed, 0, 0)
			if exact || (idx >= 0 && dist <= maxDistance) {
				if exact {
					duration := time.Since(startTime)
					logger.Debug(fmt.Sprintf("findBestMatch: exact match found at index %d in %v", idx, duration))
					return idx, true
				}
				bestIdx, bestDistance = idx, dist
			}
		}
		// If no good match found, try the other direction
		if bestIdx == -1 || bestDistance > maxDistance {
			logger.Debug("findBestMatch: no acceptable matches by removing from end, trying to remove from front")
			matchPositions, numWordsUsed, wordsRemovedFromFront, removedPrefixLen = tryRemoveFromStart(words, haystack, logger)
			if len(matchPositions) > 0 {
				idx, dist, exact := evaluateCandidates(matchPositions, numWordsUsed, wordsRemovedFromFront, removedPrefixLen)
				if exact {
					duration := time.Since(startTime)
					logger.Debug(fmt.Sprintf("findBestMatch: exact match found at index %d in %v", idx, duration))
					return idx, true
				}
				if idx >= 0 && (bestIdx == -1 || dist < bestDistance) {
					bestIdx, bestDistance = idx, dist
				}
			}
		}
	} else {
		matchPositions, numWordsUsed, wordsRemovedFromFront, removedPrefixLen = tryRemoveFromStart(words, haystack, logger)
		if len(matchPositions) > 0 {
			idx, dist, exact := evaluateCandidates(matchPositions, numWordsUsed, wordsRemovedFromFront, removedPrefixLen)
			if exact || (idx >= 0 && dist <= maxDistance) {
				if exact {
					duration := time.Since(startTime)
					logger.Debug(fmt.Sprintf("findBestMatch: exact match found at index %d in %v", idx, duration))
					return idx, true
				}
				bestIdx, bestDistance = idx, dist
			}
		}
		// If no good match found, try the other direction
		if bestIdx == -1 || bestDistance > maxDistance {
			logger.Debug("findBestMatch: no acceptable matches by removing from front, trying to remove from end")
			matchPositions, numWordsUsed = tryRemoveFromEnd(words, haystack, logger)
			if len(matchPositions) > 0 {
				idx, dist, exact := evaluateCandidates(matchPositions, numWordsUsed, 0, 0)
				if exact {
					duration := time.Since(startTime)
					logger.Debug(fmt.Sprintf("findBestMatch: exact match found at index %d in %v", idx, duration))
					return idx, true
				}
				if idx >= 0 && (bestIdx == -1 || dist < bestDistance) {
					bestIdx, bestDistance = idx, dist
				}
			}
		}
	}

	// Last resort - try middle words
	if bestIdx == -1 || bestDistance > maxDistance {
		logger.Debug("findBestMatch: no acceptable matches by removing from end or front, trying 3 middle words")
		matchPositions, numWordsUsed, wordsRemovedFromFront, removedPrefixLen = tryMiddle(words, haystack, logger)
		if len(matchPositions) > 0 {
			idx, dist, exact := evaluateCandidates(matchPositions, numWordsUsed, wordsRemovedFromFront, removedPrefixLen)
			if exact {
				duration := time.Since(startTime)
				logger.Debug(fmt.Sprintf("findBestMatch: exact match found at index %d in %v", idx, duration))
				return idx, true
			}
			if idx >= 0 && (bestIdx == -1 || dist < bestDistance) {
				bestIdx, bestDistance = idx, dist
			}
		}
	}

	// Check if we found an acceptable match
	if bestIdx >= 0 && bestDistance <= maxDistance {
		duration := time.Since(startTime)
		logger.Debug(fmt.Sprintf("findBestMatch: fuzzy match found at index %d with distance %d in %v", bestIdx, bestDistance, duration))
		return bestIdx, true
	}

	duration := time.Since(startTime)
	logger.Warn(fmt.Sprintf("findBestMatch: no acceptable match found (bestDistance=%d > maxDistance=%d) in %v. Needle:\n%s\n", bestDistance, maxDistance, duration, needle))
	return -1, false
}

func getSpeechByStartAndEnd(firstSentences string, lastSentences string, protocol *Protocol, logger *Logger) (string, error) {
	logger.Debug(fmt.Sprintf("getSpeechByStartAndEnd called with firstSentences (len=%d), lastSentences (len=%d)", len(firstSentences), len(lastSentences)))
	if protocol == nil {
		return "", fmt.Errorf("protocol cannot be nil")
	}

	text := protocol.Text

	// Try exact match first
	startIdx := strings.Index(text, firstSentences)
	if startIdx == -1 {
		// Fall back to fuzzy matching with 30% tolerance
		// For the beginning of a speech, try removing from end first (we have the start, so end might be cut off)
		logger.Debug("Exact match failed for start, trying fuzzy match")
		var found bool
		startIdx, found = findBestMatch(firstSentences, text, 0.25, removeFromEndFirst, logger) //TODO: test with 0.20
		if !found {
			// Log detailed information for debugging - this is expected to happen sometimes
			logger.Warn(fmt.Sprintf("No match found for start (skipping speech). Start text:\n%s\nEnd text:\n%s", firstSentences, lastSentences))
			return "", fmt.Errorf("could not find start of speech - skipping")
		}
		logger.Info(fmt.Sprintf("Found fuzzy match for start at index %d", startIdx))
	}

	// Search for end only after the start
	endSearchText := text[startIdx:]
	endIdx := strings.LastIndex(endSearchText, lastSentences)

	if endIdx == -1 {
		// Fall back to fuzzy matching for end
		// For the end of a speech, try removing from start first (we have the end, so start might be cut off)
		logger.Debug("Exact match failed for end, trying fuzzy match")
		var found bool
		endIdx, found = findBestMatch(lastSentences, endSearchText, 0.25, removeFromStartFirst, logger)
		if !found {
			// Log detailed information for debugging - this is expected to happen sometimes
			protocolPreview := endSearchText
			if len(protocolPreview) > 1000 {
				protocolPreview = protocolPreview[:1000] + "..."
			}
			logger.Warn(fmt.Sprintf("No match found for end (skipping speech). Start text:\n%s\nEnd text:\n%s", firstSentences, lastSentences))
			return "", fmt.Errorf("could not find end of speech - skipping")
		}
		logger.Info(fmt.Sprintf("Found fuzzy match for end at relative index %d", endIdx))
	}

	endIdx = startIdx + endIdx + len(lastSentences)

	if endIdx <= startIdx {
		logger.Warn(fmt.Sprintf("Invalid speech boundaries: start=%d, end=%d. Start text:\n%s\nEnd text:\n%s", startIdx, endIdx, firstSentences, lastSentences))
		return "", fmt.Errorf("invalid speech boundaries: start=%d, end=%d", startIdx, endIdx)
	}

	logger.Debug(fmt.Sprintf("Extracted speech from index %d to %d (length=%d)", startIdx, endIdx, endIdx-startIdx))
	return text[startIdx:endIdx], nil
}
