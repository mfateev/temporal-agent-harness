package cli

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/api/serviceerror"
)

func TestClassifyPollError_NotFound(t *testing.T) {
	err := serviceerror.NewNotFound("workflow not found")
	assert.Equal(t, pollErrorCompleted, classifyPollError(err))
}

func TestClassifyPollError_WorkflowNotReady(t *testing.T) {
	err := &serviceerror.WorkflowNotReady{Message: "workflow not ready"}
	assert.Equal(t, pollErrorTransient, classifyPollError(err))
}

func TestClassifyPollError_QueryFailed(t *testing.T) {
	err := &serviceerror.QueryFailed{Message: "query rejected"}
	assert.Equal(t, pollErrorTransient, classifyPollError(err))
}

func TestClassifyPollError_AlreadyCompleted(t *testing.T) {
	err := fmt.Errorf("workflow execution already completed")
	assert.Equal(t, pollErrorCompleted, classifyPollError(err))
}

func TestClassifyPollError_UnknownError(t *testing.T) {
	err := fmt.Errorf("some unexpected error")
	assert.Equal(t, pollErrorFatal, classifyPollError(err))
}

func TestClassifyPollError_WrappedNotFound(t *testing.T) {
	inner := serviceerror.NewNotFound("workflow not found")
	err := fmt.Errorf("query failed: %w", inner)
	assert.Equal(t, pollErrorCompleted, classifyPollError(err))
}
