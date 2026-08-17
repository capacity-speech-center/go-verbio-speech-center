package verbio_speech_center

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"verbio_speech_center/log"
	sttv1 "verbio_speech_center/proto/speechcenter/stt"

	"google.golang.org/grpc"
)

// MaxSpeechCompleteTimeoutMs is the longest end-of-speech silence the service
// accepts. Anything above it is refused there, so it is refused here too.
const MaxSpeechCompleteTimeoutMs = uint32(5000)

func (r *Recogniser) RecogniseWithGrammar(audioFile string, grammarFile string, language string, wordBoosting []string) (string, error) {
	log.Logger.Infof("Performing Grammar recognition [audioFile=%s] [grammarFile=%s] [language=%s] [wordBoosting=%v]", audioFile, grammarFile, language, wordBoosting)

	if grammarFile != "" {
		grammar, err := loadGrammar(grammarFile)
		if err != nil {
			return "", errors.New(fmt.Sprintf("error loading grammar: %+v", err))
		}

		configuration := generateGrammarRequest(grammar, language, wordBoosting)
		return r.performRecognition(audioFile, configuration)

	} else {
		return "", errors.New("received an empty grammarFile path")
	}
}

type TopicRecognition struct {
	Topic                 string
	TopicName             string
	Language              string
	Provider              string
	WordBoosting          []string
	SpeechCompleteTimeout uint32
}

func (r *Recogniser) RecogniseWithTopic(audioFile string, recognition TopicRecognition) (string, error) {
	log.Logger.Infof("Performing Topic recognition [audioFile=%s] [topic=%s] [topicName=%s] [language=%s] [provider=%s] [wordBoosting=%v] [speechCompleteTimeout=%s]",
		audioFile, recognition.Topic, recognition.TopicName, recognition.Language, recognition.Provider,
		recognition.WordBoosting, formatSpeechCompleteTimeout(recognition.SpeechCompleteTimeout))
	configuration, err := generateTopicRequest(recognition)
	if err != nil {
		return "", errors.New(fmt.Sprintf("error creating topic request: %+v", err))
	}
	return r.performRecognition(audioFile, configuration)
}

type recogResult struct {
	recognition string
	err         error
}

func (r *Recogniser) performRecognition(audioFile string, configuration *sttv1.RecognitionStreamingRequest) (string, error) {
	audio, err := loadAudio(audioFile)
	if err != nil {
		return "", errors.New(fmt.Sprintf("error loading audio file %+v", err))
	}

	r.streamClient, err = r.client.StreamingRecognize(context.Background(), grpc.WaitForReady(true))
	if err != nil {
		return "", errors.New(fmt.Sprintf("error obtaining streaming client: %+v", err))
	}

	c := make(chan recogResult)
	go func() {
		c = r.collectResponses(c)
	}()

	if err = r.sendAudio(configuration, audio); err != nil {
		return "", err
	}

	log.Logger.Info("Waiting for recognition to finish")
	recog := <-c
	if recog.err != nil {
		return "", errors.New(fmt.Sprintf("got error during recognition: %+v", recog.err))
	}

	return recog.recognition, nil
}

func (r *Recogniser) collectResponses(c chan recogResult) chan recogResult {
	recog := make([]string, 0)
	log.Logger.Debugf("> Waiting for responses ...")
	totalAudioLengthInMs := float32(0)
	for {
		resp := &sttv1.RecognitionStreamingResponse{}
		err := r.streamClient.RecvMsg(resp)
		if err != nil {
			if err == io.EOF {
				log.Logger.Debugf("Got EOF")
				c <- recogResult{recognition: strings.Join(recog, " "), err: nil}
				break
			} else {
				log.Logger.Debugf("Got result")
				c <- recogResult{recognition: "", err: err}
				break
			}
		} else {
			// Check for errors in response
			if resp.GetError() != nil {
				errMsg := fmt.Sprintf("recognition error: %s (domain: %s)", resp.GetError().Reason, resp.GetError().Domain)
				c <- recogResult{recognition: "", err: errors.New(errMsg)}
				break
			}
			// Extract transcript from result
			if result := resp.GetResult(); result != nil && len(result.Alternatives) > 0 {
				log.Logger.Debugf("Got partial recog: %s (is_final: %v) (is_endpoint: %v) (silence: %d ms)",
					result.Alternatives[0].Transcript, result.IsFinal, result.IsEndpoint,
					r.calculateEndOfUtteranceSilence(result, totalAudioLengthInMs))
				if result.IsFinal {
					recog = append(recog, markEndpoint(result.Alternatives[0].Transcript, result.IsEndpoint))
					totalAudioLengthInMs += result.Duration
				}
			}
		}
	}
	log.Logger.Debugf("< all responses received")
	return c
}

func markEndpoint(transcript string, isEndpoint bool) string {
	transcript = strings.TrimSpace(transcript)
	if isEndpoint {
		return strings.TrimSpace(transcript + " <endpoint>")
	}
	return transcript
}

func (r *Recogniser) calculateEndOfUtteranceSilence(result *sttv1.RecognitionResult, totalAudioLengthInMs float32) int32 {
	words := result.Alternatives[0].Words
	finalSilenceInMs := int32(0)
	if len(words) > 0 {
		finalSilenceInMs = int32((totalAudioLengthInMs + result.Duration - words[len(words)-1].EndTime) * 1000)
	}
	return finalSilenceInMs
}

func (r *Recogniser) sendAudio(configuration *sttv1.RecognitionStreamingRequest, audio []byte) error {
	log.Logger.Info("Sending configuration request")
	if err := r.streamClient.Send(configuration); err != nil {
		return errors.New(fmt.Sprintf("error sending configuration request: %+v", err))
	}

	if err := r.sendAudioStream(audio); err != nil {
		return err
	}

	if err := r.streamClient.CloseSend(); err != nil {
		return errors.New(fmt.Sprintf("error closing send: %+v", err))
	}
	return nil
}

func (r *Recogniser) sendAudioStream(audio []byte) error {
	log.Logger.Info("Sending audio stream.")
	if err := r.sendAudioChunks(audio); err != nil {
		return errors.New(fmt.Sprintf("error sending Audio chunks: %+v", err))
	}
	if err := r.sendEndOfStream(); err != nil {
		return errors.New(fmt.Sprintf("error sending END_OF_STREAM event: %+v", err))
	}
	return nil
}

func (r *Recogniser) sendAudioChunks(audio []byte) error {
	const chunkSize = 800
	for i := 0; i < len(audio); i += chunkSize {
		end := i + chunkSize
		if end > len(audio) {
			end = len(audio)
		}

		audioChunk := audio[i:end]
		err := r.SendAudioRequest(audioChunk)
		if err != nil {
			return errors.New(fmt.Sprintf("error sending audio chunk: %+v", err))
		}
	}
	return nil
}

func (r *Recogniser) sendEndOfStream() error {
	// Send END_OF_STREAM event
	log.Logger.Info("Sending END_OF_STREAM event")
	endOfStreamRequest := &sttv1.RecognitionStreamingRequest{
		RecognitionRequest: &sttv1.RecognitionStreamingRequest_EventMessage{
			EventMessage: &sttv1.EventMessage{
				Event: sttv1.EventMessage_END_OF_STREAM,
			},
		},
	}
	return r.streamClient.Send(endOfStreamRequest)
}

func (r *Recogniser) SendAudioRequest(audioChunk []byte) error {
	log.Logger.Tracef("Sending audio chunk (size: %d bytes)", len(audioChunk))
	const sampleRate = int32(8000)
	endOfRequest := time.Now().Add(time.Duration(float64(len(audioChunk)) / float64(sampleRate) * float64(time.Second)))
	audioRequest := &sttv1.RecognitionStreamingRequest{
		RecognitionRequest: &sttv1.RecognitionStreamingRequest_Audio{
			Audio: audioChunk,
		},
	}

	log.Logger.Tracef("Audio chunk will be sent until %d", time.Until(endOfRequest).Milliseconds())
	time.Sleep(time.Until(endOfRequest))
	return r.streamClient.Send(audioRequest)
}

func formatSpeechCompleteTimeout(speechCompleteTimeout uint32) string {
	if speechCompleteTimeout == 0 {
		return "unset"
	}
	return fmt.Sprintf("%dms", speechCompleteTimeout)
}

func generateTimerConfiguration(speechCompleteTimeout uint32) *sttv1.TimerConfiguration {
	if speechCompleteTimeout == 0 {
		return nil
	}
	return &sttv1.TimerConfiguration{
		SpeechCompleteTimeout: &speechCompleteTimeout,
	}
}

func generateGrammarRequest(grammar []byte, language string, wordBoosting []string) *sttv1.RecognitionStreamingRequest {
	sampleRate := uint32(8000)

	resource := &sttv1.RecognitionResource{
		Resource: &sttv1.RecognitionResource_Grammar{
			Grammar: &sttv1.GrammarResource{
				Grammar: &sttv1.GrammarResource_CompiledGrammar{
					CompiledGrammar: grammar,
				},
			},
		},
	}

	config := &sttv1.RecognitionConfig{
		Parameters: &sttv1.RecognitionParameters{
			Language: language,
			AudioEncoding: &sttv1.RecognitionParameters_Pcm{
				Pcm: &sttv1.PCM{
					SampleRateHz: sampleRate,
				},
			},
			WordBoosting: wordBoosting,
		},
		Resource: resource,
		Version:  sttv1.RecognitionConfig_V2,
	}

	return &sttv1.RecognitionStreamingRequest{
		RecognitionRequest: &sttv1.RecognitionStreamingRequest_Config{
			Config: config,
		},
	}
}

func generateTopicRequest(recognition TopicRecognition) (*sttv1.RecognitionStreamingRequest, error) {
	recognition, err := normalizeTopicRecognition(recognition)
	if err != nil {
		return nil, err
	}

	resource, err := generateTopicResource(recognition)
	if err != nil {
		return nil, err
	}

	// Default sample rate for speech recognition (16kHz is common)
	sampleRate := uint32(8000)

	config := &sttv1.RecognitionConfig{
		Parameters: &sttv1.RecognitionParameters{
			Language: recognition.Language,
			AudioEncoding: &sttv1.RecognitionParameters_Pcm{
				Pcm: &sttv1.PCM{
					SampleRateHz: sampleRate,
				},
			},
			WordBoosting: recognition.WordBoosting,
		},
		Resource:      resource,
		Configuration: generateTimerConfiguration(recognition.SpeechCompleteTimeout),
	}

	if recognition.Provider != "" {
		config.Provider = &recognition.Provider
	} else {
		config.Version = sttv1.RecognitionConfig_V2
	}

	return &sttv1.RecognitionStreamingRequest{
		RecognitionRequest: &sttv1.RecognitionStreamingRequest_Config{
			Config: config,
		},
	}, nil
}

func normalizeTopicRecognition(recognition TopicRecognition) (TopicRecognition, error) {
	recognition.TopicName = strings.ToLower(strings.TrimSpace(recognition.TopicName))

	if recognition.SpeechCompleteTimeout > MaxSpeechCompleteTimeoutMs {
		return recognition, errors.New(fmt.Sprintf("speech complete timeout of %dms is above the %dms maximum",
			recognition.SpeechCompleteTimeout, MaxSpeechCompleteTimeoutMs))
	}

	return recognition, nil
}

func generateTopicResource(recognition TopicRecognition) (*sttv1.RecognitionResource, error) {
	if recognition.TopicName != "" {
		log.Logger.Infof("Performing recognition with topic name: %s", recognition.TopicName)
		return &sttv1.RecognitionResource{
			Resource: &sttv1.RecognitionResource_TopicName{
				TopicName: recognition.TopicName,
			},
		}, nil
	}

	topicLower := strings.ToLower(recognition.Topic)
	if topicLower != "generic" {
		return nil, errors.New(fmt.Sprintf("unrecognized topic: %s (only 'generic' is supported)", recognition.Topic))
	}

	log.Logger.Infof("Performing recognition with topic: %s", topicLower)
	return &sttv1.RecognitionResource{
		Resource: &sttv1.RecognitionResource_Topic_{
			Topic: sttv1.RecognitionResource_GENERIC,
		},
	}, nil
}

func loadAudio(file string) ([]byte, error) {
	contents, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("error reading audio file: %+v", err))
	}

	return contents, nil
}

func loadGrammar(file string) ([]byte, error) {
	contents, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("error reading grammar file: %+v", err))
	}

	return contents, nil
}
