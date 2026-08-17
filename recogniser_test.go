package verbio_speech_center

import (
	"os"
	"path/filepath"
	"testing"
	sttv1 "verbio_speech_center/proto/speechcenter/stt"

	"github.com/stretchr/testify/assert"
)

func TestNewRecogniser(t *testing.T) {
	recognizer, err := NewRecogniser("localhost:50051", createTemporaryToken(t))
	assert.NoError(t, err)
	assert.NotNil(t, recognizer)
	assert.NotNil(t, recognizer.conn)
	assert.NotNil(t, recognizer.client)
	assert.Nil(t, recognizer.streamClient)

	err = recognizer.Close()
	assert.NoError(t, err)
}

func TestGenerateTopicRequestWithSpeechCompleteTimeout(t *testing.T) {
	timeout := uint32(1200)

	topicRequest, err := generateTopicRequest(TopicRecognition{TopicName: "generic", Language: "es", SpeechCompleteTimeout: timeout})
	assert.NoError(t, err)
	assert.Equal(t, timeout, topicRequest.GetConfig().GetConfiguration().GetSpeechCompleteTimeout())
}

func TestGenerateTopicRequestWithoutSpeechCompleteTimeoutLeavesItUnset(t *testing.T) {
	topicRequest, err := generateTopicRequest(TopicRecognition{TopicName: "generic", Language: "es"})
	assert.NoError(t, err)
	assert.Nil(t, topicRequest.GetConfig().GetConfiguration())
}

func TestGenerateTopicRequestUsesTopicName(t *testing.T) {
	topicRequest, err := generateTopicRequest(TopicRecognition{TopicName: "  Conversational_AI  ", Language: "es"})
	assert.NoError(t, err)

	config := topicRequest.GetConfig()
	assert.Equal(t, "conversational_ai", config.GetResource().GetTopicName())
	// The oneof must carry the name, not the deprecated enum.
	assert.IsType(t, &sttv1.RecognitionResource_TopicName{}, config.GetResource().GetResource())
}

// The deprecated enum is only read when no topic name is given.
func TestGenerateTopicRequestFallsBackToDeprecatedTopic(t *testing.T) {
	topicRequest, err := generateTopicRequest(TopicRecognition{Topic: "GENERIC", Language: "es"})
	assert.NoError(t, err)
	assert.Equal(t, sttv1.RecognitionResource_GENERIC, topicRequest.GetConfig().GetResource().GetTopic())
	assert.Empty(t, topicRequest.GetConfig().GetResource().GetTopicName())

	_, err = generateTopicRequest(TopicRecognition{Topic: "BANKING", Language: "es"})
	assert.Error(t, err)
}

func TestGenerateTopicRequestWhitespaceTopicNameFallsBackToDeprecatedTopic(t *testing.T) {
	topicRequest, err := generateTopicRequest(TopicRecognition{TopicName: "   ", Topic: "GENERIC", Language: "es"})
	assert.NoError(t, err)

	resource := topicRequest.GetConfig().GetResource()
	assert.IsType(t, &sttv1.RecognitionResource_Topic_{}, resource.GetResource())
	assert.Equal(t, sttv1.RecognitionResource_GENERIC, resource.GetTopic())
	assert.Empty(t, resource.GetTopicName())
}

func TestGenerateTopicRequestWhitespaceTopicNameWithoutTopicIsRejected(t *testing.T) {
	_, err := generateTopicRequest(TopicRecognition{TopicName: "   ", Language: "es"})
	assert.Error(t, err)
}

func TestGenerateTopicRequestRejectsSpeechCompleteTimeoutAboveMaximum(t *testing.T) {
	_, err := generateTopicRequest(TopicRecognition{
		TopicName:             "generic",
		Language:              "es",
		SpeechCompleteTimeout: MaxSpeechCompleteTimeoutMs + 1,
	})
	assert.Error(t, err)

	// The maximum itself is still a valid request.
	atMaximum, err := generateTopicRequest(TopicRecognition{
		TopicName:             "generic",
		Language:              "es",
		SpeechCompleteTimeout: MaxSpeechCompleteTimeoutMs,
	})
	assert.NoError(t, err)
	assert.Equal(t, MaxSpeechCompleteTimeoutMs, atMaximum.GetConfig().GetConfiguration().GetSpeechCompleteTimeout())
}

func TestGenerateTopicRequestProviderReplacesVersion(t *testing.T) {
	withProvider, err := generateTopicRequest(TopicRecognition{TopicName: "generic", Language: "es", Provider: "capacity"})
	assert.NoError(t, err)
	assert.Equal(t, "capacity", withProvider.GetConfig().GetProvider())

	// Without a provider the deprecated version still drives the implicit routing.
	withoutProvider, err := generateTopicRequest(TopicRecognition{TopicName: "generic", Language: "es"})
	assert.NoError(t, err)
	assert.Empty(t, withoutProvider.GetConfig().GetProvider())
	assert.Equal(t, sttv1.RecognitionConfig_V2, withoutProvider.GetConfig().GetVersion())
}

func TestGenerateGrammarRequestCarriesNoTimerConfiguration(t *testing.T) {
	grammarRequest := generateGrammarRequest([]byte("grammar"), "es", nil)
	assert.Nil(t, grammarRequest.GetConfig().GetConfiguration())
}

func TestMarkEndpoint(t *testing.T) {
	assert.Equal(t, "hola <endpoint>", markEndpoint(" hola ", true))
	assert.Equal(t, "hola", markEndpoint(" hola ", false))
	assert.Equal(t, "<endpoint>", markEndpoint("", true))
}

func createTemporaryToken(t *testing.T) string {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token.txt")
	err := os.WriteFile(tokenFile, []byte("test-token"), 0600)
	assert.NoError(t, err)
	return tokenFile
}

func TestNewRecogniserErrors(t *testing.T) {
	recognizer, err := NewRecogniser("localhost", "non-existent-file")
	assert.Error(t, err)
	assert.Nil(t, recognizer)

	recognizer, err = NewRecogniser("invalid-url:", createTemporaryToken(t))
	assert.Error(t, err)
	assert.Nil(t, recognizer)

}

func TestNonExistentToken(t *testing.T) {
	recognizer, err := NewRecogniser("localhost:50051", "non-existent-file")
	assert.Error(t, err)
	assert.Nil(t, recognizer)
}

func TestEmptyToken(t *testing.T) {
	recognizer, err := NewRecogniser("invalid-url", "")
	assert.Error(t, err)
	assert.Nil(t, recognizer)
}

func TestLoadToken(t *testing.T) {
	token, err := loadToken(createTemporaryToken(t))
	assert.NoError(t, err)
	assert.Equal(t, "test-token", token)
}

func TestNotExistentFile(t *testing.T) {
	token, err := loadToken("non-existent-file")
	assert.Error(t, err)
	assert.Empty(t, token)
}

func TestClose(t *testing.T) {
	recognizer, err := NewRecogniser("localhost:50051", createTemporaryToken(t))
	assert.NoError(t, err)

	err = recognizer.Close()
	assert.NoError(t, err)
}
