package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// CompleteSpeech represents a speech that both starts and ends within a chunk
type CompleteSpeech struct {
	Speaker         string `json:"speaker"`
	SpeechTextStart string `json:"speech_text_start"`
	SpeechTextEnd   string `json:"speech_text_end"`
	GivenToProtocol bool   `json:"given_to_protocol"`
}

// StartedSpeech represents a speech that has started but is not yet complete
type StartedSpeech struct {
	Present           bool   `json:"present"`
	Speaker           string `json:"speaker"`
	SpeechTextStart   string `json:"speech_text_start"`
	BeginningTooShort bool   `json:"beginning_too_short"`
	GivenToProtocol   bool   `json:"given_to_protocol"`
}

// SpeechExtractionResponse represents the full response from the model
type SpeechExtractionResponse struct {
	CompleteSpeeches []CompleteSpeech `json:"complete_speeches"`
	StartedSpeech    *StartedSpeech   `json:"started_speech"`
}

type ModelInterface interface {
	SetSystemInstruction(systemInstruction string) //Only use to overwrite system instruction with custom one
	SetResponseSchema()                            //Response schema must be hardcoded in model implementation
	Initialize(logger *Logger) error               //The hardcoded system instruction from this class is used
	GenerateContent(query []string, logger *Logger) (*SpeechExtractionResponse, error)
}

const systemInstruction = `
You will receive a chunk of a German parliamentary protocol along with the end of the previous chunk. Your task is to extract speeches.
A speech is the full statement of one person, including any interruptions (e.g., questions or interjections by others), WHICH ALWAYS SHOULD BE INCLUDED AS PART OF THE SPEECH.

1. Identify speeches that start and end within this chunk (not the previous chunk). For each, return the speaker's full name, the first 15-30 words, and the last 15-30 words of the speech. If the beginning is very generic use closer to 30 words, else closer to 15.
2. If a speech starts in this chunk but does not end, return the speaker's full name and the first 15-30 words if possible.
3. If a speech started in a previous chunk and continues here, you already received the beginning of the speech. If the beginning is flagged as too short or generic, expand it with the beginning of this chunk until it is 15-30 words long in total. Make sure to not add any spaces or similar, the result should be verbatim from the original text.
Return the speech in the completed section if it is complete, or in the "started" section if it is still not complete.

"Speaker's full name" refers to the format "FirstName LastName" with no titles, honorifics, or party affiliations.
Speeches always begin with the speaker being named (e.g., "Dr. Angela Merkel (CDU/CSU):" or "Manuela Schwesig (Mecklenburg-Vorpommern):").
Note that questions or interjections also begin with a speaker name and should be included as part of the ongoing speech rather than separated.
DO NOT INCLUDE organizational remarks by the president or vice president of the parliament, they are not speeches.
'Erklärungen' and 'zu Protokoll gegebene Reden' also count as speeches if its done for or by a natural person. If the Erklärung or Rede is given for somebody else, affiliate it with that person. For example "Für Frau Ministerin Mona Neubaur gebe ich folgende
Erklärung zu Protokoll" would be a speech for Mona Neubaur. "Für die Länder Hamburg und Bremen gebe ich folgende Erklärung zu Protokoll" is not done for or by a natural person, and therefore must be ignored. Only the speech or erklärung itself count, not the announcment that there generally is one.

You must return the first 15-30 words and the last 15-30 words of each speech in the original formatting, so I can programatically reconstruct the full speech.
This uses less tokens than you returning the full speech. Therefore you must be CAREFUL TO MATCH EXACTLY THE ORIGINAL FORMATTING INCLUDING ANY ARTIFACTS LIKE NBSP AND ESPECIALLY NEWLINES.

CRITICAL RULES - EXACT SUBSTRING EXTRACTION:
- Copy text EXACTLY character by character from the original
- DO NOT use ".." or "..." (ellipses) to skip or summarize parts
- NEVER START OR END A TEXT WITH ".." or "..." (ellipses), except if this is actually in the original text
- DO NOT paraphrase, reformat, or reconstruct sentences
- DO NOT add or remove any characters, spaces, or newlines
- DO NOT CHANGE THE CASING OF ANY LETTER
- NEVER SKIP SPEECHES, WE NEED ALL OF THEM.
- EVERY character must match the original PERFECTLY
- The rule is: AS FEW WORDS AS POSSIBLE, BUT AS MANY AS NECESSARY TO IDENTIFY THE SPEECH; NEVER MORE THAN 30 WORDS.
`

// This is an exemplary response schema for gemini. If you want to change the response schema
// change it here, and have an LLM adjust it for each model implementation
func GetExemplaryResponseSchema() genai.Schema {
	return genai.Schema{
		Type:        "object",
		Description: "Container for iterative speech extraction and assignment to activities.",
		Properties: map[string]*genai.Schema{
			"complete_speeches": {
				Type:        "array",
				Description: "List of speeches that both start and end within this chunk.",
				Items: &genai.Schema{
					Type:     "object",
					Required: []string{"speaker", "speech_text_start", "speech_text_end", "given_to_protocol"},
					Properties: map[string]*genai.Schema{
						"speaker": {
							Type:        "string",
							Description: "The full name of the speaker that gave the speech. Do NOT INCLUDE titles like 'Dr.' or 'Prof.'",
						},
						"speech_text_start": {
							Type:        "string",
							Description: "EXACT substring: The first 15-30 words (NOT MORE) of the speech copied character-by-character from the original text. NO ellipsis (..), NO paraphrasing, NO changes. Must include exact formatting with NBSP, newlines, and all artifacts. Also include interruptions by others like interjections or clapping if they are in the original text.",
						},
						"speech_text_end": {
							Type:        "string",
							Description: "EXACT substring: The last 15-30 words (NOT MORE) of the speech copied character-by-character from the original text. NO ellipsis (..), NO paraphrasing, NO changes. Must include exact formatting with NBSP, newlines, and all artifacts. Also include interruptions by others like interjections or clapping if they are in the original text.",
						},
						"given_to_protocol": {
							Type:        "boolean",
							Description: "Indicates if the speech is a 'zu Protokoll gegebene Rede', so if its given to protocol and read by somebody else. Only true if its clearly given to protocol AND IF IT IS A FULL SPEECH AND NOT ONLY THE ANNOUNCEMENT. False if the speech is a regular live speech, which is the regular case.",
						},
					},
				},
			},
			"started_speech": {
				Type:        "object",
				Description: "Contains the beginning part of a speech that has started in this chunk but is not yet complete.",
				Properties: map[string]*genai.Schema{
					"present": {
						Type:        "boolean",
						Description: "Indicates if there is a speech that has started but is not yet complete.",
					},
					"speaker": {
						Type:        "string",
						Description: "The full name of the speaker that gave the started speech. Do NOT INCLUDE titles like 'Dr.' or 'Prof.'",
					},
					"speech_text_start": {
						Type:        "string",
						Description: "EXACT substring: The first 15-30 words of the speech copied character-by-character from the original text. MUST be truncated after 30 words. DO NOT return the complete speech. NO ellipsis (..), NO paraphrasing, NO changes. Must include exact formatting with NBSP, newlines, and all artifacts. Also include interruptions by others like interjections or clapping if they are in the original text.",
					},
					"beginning_too_short": {
						Type:        "boolean",
						Description: "Indicates if the beginning of the speech is too short or generic because the chunk ended soon after the beginning. Only set to true if the beginning is very likely too generic to identify the text. ONLY do this if you are really sure that the beginning is too short, as this can lead to errors.",
					},
					"given_to_protocol": {
						Type:        "boolean",
						Description: "Indicates if the speech is a 'zu Protokoll gegebene Rede', so if its given to protocol and read by somebody else. Only true if its clearly given to protocol AND IS A FULL SPEECH AND NOT ONLY THE ANNOUNCEMENT (!!!!!). False if the speech is a regular live speech, which is the regular case.",
					},
				},
			},
		},
	}
}

// ParseModelResponse is a helper function that model implementations can use to parse JSON responses
// into SpeechExtractionResponse. It handles validation and logging of missing/invalid data.
// The responseText should be the raw JSON string returned by the model API.
func ParseModelResponse(responseText string, logger *Logger) (*SpeechExtractionResponse, error) {
	var response SpeechExtractionResponse
	if err := json.Unmarshal([]byte(responseText), &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal speech extraction response: %w", err)
	}
	var prettyResponse bytes.Buffer
	if err := json.Indent(&prettyResponse, []byte(responseText), "", "  "); err != nil {
		logger.Debug(fmt.Sprintf("Parsed model response (raw): %s", responseText))
	} else {
		logger.Debug(fmt.Sprintf("Parsed model response (pretty):\n%s", prettyResponse.String()))
	}

	if response.CompleteSpeeches == nil {
		logger.Warn("complete_speeches is missing or null, treating as empty array")
		response.CompleteSpeeches = []CompleteSpeech{}
	}

	for i, speech := range response.CompleteSpeeches {
		if speech.Speaker == "" {
			logger.Warn(fmt.Sprintf("complete_speeches[%d] has empty speaker, skipping", i))
		}
		if speech.SpeechTextStart == "" {
			logger.Warn(fmt.Sprintf("complete_speeches[%d] has empty speech_text_start for speaker %s, skipping", i, speech.Speaker))
		}
		if speech.SpeechTextEnd == "" {
			logger.Warn(fmt.Sprintf("complete_speeches[%d] has empty speech_text_end for speaker %s, skipping", i, speech.Speaker))
		}
	}

	// Validate started speech if present
	if response.StartedSpeech != nil {
		if !response.StartedSpeech.Present {
			response.StartedSpeech = nil
		} else if response.StartedSpeech.Speaker == "" {
			logger.Warn("started_speech.present is true but speaker is empty, treating as not present")
			response.StartedSpeech = nil
		} else if response.StartedSpeech.SpeechTextStart == "" {
			logger.Warn(fmt.Sprintf("started_speech.present is true but speech_text_start is empty for speaker %s, treating as not present", response.StartedSpeech.Speaker))
			response.StartedSpeech = nil
		}
	}

	return &response, nil
}
