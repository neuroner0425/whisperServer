//go:build ignore
// +build ignore

package main

import (
  "context"
  "flag"
  "fmt"
  "log"

  "google.golang.org/genai"
)

var model = flag.String("model", "gemini-3.1-flash-lite-preview", "the model name, e.g. gemini-3.1-flash-lite-preview")

func run(ctx context.Context) {
  client, err := genai.NewClient(ctx, &genai.ClientConfig{
    Backend: genai.BackendGeminiAPI,
    APIKey: os.Getenv("GEMINI_API_KEY"),
  })
  if err != nil {
    log.Fatal(err)
  }


  var responseSchema genai.Schema
  err := json.Unmarshal([]byte(`{
          "type": "object",
          "properties": {
            "paragraph": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "paragraph_summary": {
                    "type": "string"
                  },
                  "sentence": {
                    "type": "array",
                    "items": {
                      "type": "object",
                      "properties": {
                        "start_time": {
                          "type": "string"
                        },
                        "content": {
                          "type": "string"
                        }
                      },
                      "required": [
                        "start_time",
                        "content"
                      ],
                      "propertyOrdering": [
                        "start_time",
                        "content"
                      ]
                    }
                  }
                },
                "required": [
                  "paragraph_summary",
                  "sentence"
                ],
                "propertyOrdering": [
                  "paragraph_summary",
                  "sentence"
                ]
              }
            }
          },
          "required": [
            "paragraph"
          ],
          "propertyOrdering": [
            "paragraph"
          ]
        }`), &responseSchema)
  if err != nil {
    log.Fatal(err)
  }
  var config *genai.GenerateContentConfig = &genai.GenerateContentConfig{
    Temperature: genai.Ptr[float32](0.5),
    SystemInstruction: &genai.Content{
      Parts: []*genai.Part{
        &genai.Part{Text: "# Role\nYou are a professional 'Speech-to-Text (STT) Correction Editor.' You possess an exceptional ability to grasp the context of fragmented transcription data and refine incomplete sentences into natural, accurate spoken scripts—not formal written text. Your highest priority is to preserve every detail of the original speech without omitting any content.\n\n# Task\nThe provided [Original] text is a result of transcribing lectures or speeches using an STT engine. It contains typos, spacing errors, grammatical mistakes, and fragmented sentences. Refine this text according to the [Guidelines] below, ensuring **Zero Omission**.\n\n# Guidelines\n1. **Contextual Correction:**\n   - Correct mis-transcribed words that sound similar based on the context. (e.g., '정보의미' -> '정보 은닉', '이네이턴스' -> '상속(Inheritance)')\n   - Ensure technical terms use accurate notation (include English if necessary). Format code variables or operators according to programming syntax. (e.g., '데이터 스트럭처' -> '자료구조(Data Structure)', 'M 퍼센트' -> '&')\n\n2. **No Omission:**\n   - **Never summarize the content or shorten sentences.** Be vigilant against the tendency to merge or condense sentences toward the end of the text.\n   - Do not arbitrarily delete any part of the original speech, including the speaker's intent, small talk, additional explanations, or exclamations.\n   - Every spoken element must be included in the output. (Meaningless repetitive stammers or filler sounds may be cleaned up naturally.)\n   - Do not change the original meaning or distort facts during the refining process.\n   - The volume of the output text must be nearly identical to the volume of the original text.\n\n3. **Complete Sentence Construction:**\n   - Transform lists of fragmented words into grammatically correct sentences. Use commas (,) and periods (.) appropriately to enhance readability.\n\n4. **Contextual Paragraphing:**\n   - Group sentences that discuss a single topic into a paragraph.\n   - This means creating a paragraph that contains the refined sentences, not merging them into one long sentence. All sentences within a paragraph must be output as refined.\n   - Start a new paragraph when the topic shifts or the flow of conversation changes.\n\n# Output Format\n{\n  \"paragraph\": [\n    {\n      \"paragraph_summary\": \"문단 요약 정리\",\n      \"sentence\": [\n        {\n          \"start_time\": \"[00:00:00,000]\",\n          \"content\": \"문장 정제 내용1\"\n        },\n        ...\n      ]\n    },\n    ...\n  ]\n}"}, 
      }
    }
    ThinkingConfig: &genai.ThinkingConfig{
      ThinkingLevel: genai.Ptr[string]("HIGH"),
    },
    ResponseMimeType: "application/json",
    ResponseSchema: &responseSchema,
  }

  var contents = []*genai.Content{
    &genai.Content{
      Role: "user",
      Parts: []*genai.Part{
        &genai.Part{
          Text: "INSERT_INPUT_HERE",
        },
      },
    },
  }

  // Call the GenerateContent method.
  result, err := client.Models.GenerateContent(ctx, *model, contents, config)
  if err != nil {
    log.Fatal(err)
  }
  fmt.Println(result.Text())
}

func main() {
  ctx := context.Background()
  flag.Parse()
  run(ctx)
}
