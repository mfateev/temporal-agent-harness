package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mfateev/codex-temporal-go/internal/workflow"
)

func singleQuestionReq() *workflow.PendingUserInputRequest {
	return &workflow.PendingUserInputRequest{
		CallID: "call-1",
		Questions: []workflow.RequestUserInputQuestion{
			{
				ID:       "q1",
				Question: "Which approach?",
				Options: []workflow.RequestUserInputQuestionOption{
					{Label: "Option A", Description: "Description A"},
					{Label: "Option B", Description: "Description B"},
					{Label: "Option C", Description: "Description C"},
				},
			},
		},
	}
}

func multiQuestionReq() *workflow.PendingUserInputRequest {
	return &workflow.PendingUserInputRequest{
		CallID: "call-2",
		Questions: []workflow.RequestUserInputQuestion{
			{
				ID:       "q1",
				Question: "Which library?",
				Options: []workflow.RequestUserInputQuestionOption{
					{Label: "React", Description: "Frontend framework"},
					{Label: "Vue", Description: "Progressive framework"},
				},
			},
			{
				ID:       "q2",
				Question: "Which language?",
				Options: []workflow.RequestUserInputQuestionOption{
					{Label: "TypeScript"},
					{Label: "JavaScript"},
				},
			},
		},
	}
}

// --- Single question tests ---

func TestHandleUserInputQuestion_NumericSelection(t *testing.T) {
	resp := HandleUserInputQuestionInput("1", singleQuestionReq())
	require.NotNil(t, resp)
	require.Contains(t, resp.Answers, "q1")
	assert.Equal(t, []string{"Option A"}, resp.Answers["q1"].Answers)
}

func TestHandleUserInputQuestion_NumericSelectionLast(t *testing.T) {
	resp := HandleUserInputQuestionInput("3", singleQuestionReq())
	require.NotNil(t, resp)
	assert.Equal(t, []string{"Option C"}, resp.Answers["q1"].Answers)
}

func TestHandleUserInputQuestion_OutOfRange(t *testing.T) {
	resp := HandleUserInputQuestionInput("5", singleQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_ZeroIndex(t *testing.T) {
	resp := HandleUserInputQuestionInput("0", singleQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_NegativeIndex(t *testing.T) {
	resp := HandleUserInputQuestionInput("-1", singleQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_FreeformText(t *testing.T) {
	resp := HandleUserInputQuestionInput("custom approach", singleQuestionReq())
	require.NotNil(t, resp)
	assert.Equal(t, []string{"custom approach"}, resp.Answers["q1"].Answers)
}

func TestHandleUserInputQuestion_EmptyInput(t *testing.T) {
	resp := HandleUserInputQuestionInput("", singleQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_WhitespaceInput(t *testing.T) {
	resp := HandleUserInputQuestionInput("   ", singleQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_NilRequest(t *testing.T) {
	resp := HandleUserInputQuestionInput("1", nil)
	assert.Nil(t, resp)
}

// --- Multi question tests ---

func TestHandleUserInputQuestion_MultiQuestionNumeric(t *testing.T) {
	resp := HandleUserInputQuestionInput("1,2", multiQuestionReq())
	require.NotNil(t, resp)
	assert.Equal(t, []string{"React"}, resp.Answers["q1"].Answers)
	assert.Equal(t, []string{"JavaScript"}, resp.Answers["q2"].Answers)
}

func TestHandleUserInputQuestion_MultiQuestionWithSpaces(t *testing.T) {
	resp := HandleUserInputQuestionInput("2, 1", multiQuestionReq())
	require.NotNil(t, resp)
	assert.Equal(t, []string{"Vue"}, resp.Answers["q1"].Answers)
	assert.Equal(t, []string{"TypeScript"}, resp.Answers["q2"].Answers)
}

func TestHandleUserInputQuestion_MultiQuestionWrongCount(t *testing.T) {
	// Only 1 number for 2 questions — rejected
	resp := HandleUserInputQuestionInput("1", multiQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_MultiQuestionOutOfRange(t *testing.T) {
	resp := HandleUserInputQuestionInput("1,5", multiQuestionReq())
	assert.Nil(t, resp)
}

func TestHandleUserInputQuestion_MultiQuestionFreeform(t *testing.T) {
	resp := HandleUserInputQuestionInput("custom lib, custom lang", multiQuestionReq())
	require.NotNil(t, resp)
	assert.Equal(t, []string{"custom lib"}, resp.Answers["q1"].Answers)
	assert.Equal(t, []string{"custom lang"}, resp.Answers["q2"].Answers)
}
