package main

import (
	"fmt"
	"verbio_speech_center"
	"verbio_speech_center/constants"
	"verbio_speech_center/log"
	ttsv1 "verbio_speech_center/proto/speechcenter/tts"

	"github.com/jessevdk/go-flags"
)

const (
	BACKEND_URL = "us.speechcenter.verbio.com"
)

type Command interface {
	Execute() error
}

type GlobalOpts struct {
	LogLevel  string `short:"l" long:"log-level" description:"Log Level (must be one of TRACE DEBUG INFO WARN ERROR)" default:"info"`
	TokenFile string `short:"t" long:"token-file" description:"Path to the Token File" `
	Url       string `short:"u" long:"url" description:"Url of the service" default:""`
}

type RecognizeOpts struct {
	Audio                 string   `short:"a" long:"audio" description:"Audio file to be sent" required:"true"`
	Grammar               string   `short:"g" long:"grammar" description:"Path to the grammar to be used"`
	TopicName             string   `short:"N" long:"topic-name" description:"Topic to be used, as a free-form name (generic, medical, finance, conversational_ai, beauty, telecommunications, home_services)"`
	Topic                 string   `short:"T" long:"topic" description:"[DEPRECATED] Topic to be used. Use --topic-name instead"`
	Provider              string   `short:"p" long:"provider" description:"Speech recognition provider to use (verbio, deepgram, capacity)"`
	Language              string   `short:"L" long:"language" description:"Language to be used" default:"en-US"`
	WordBoosting          []string `short:"w" long:"word-boosting" description:"Word to boost during recognition (can be specified multiple times)"`
	SpeechCompleteTimeout uint32   `long:"speech-complete-timeout" description:"Milliseconds of silence after speech that end an utterance (from 1 to 5000). Left to the recogniser default when omitted"`
}

type SynthesizeOpts struct {
	Text         string `short:"s" long:"text" description:"Text to synthesize" required:"true"`
	Voice        string `short:"v" long:"voice" description:"Voice code to use for synthesis" required:"true"`
	SamplingRate string `long:"sampling-rate" description:"Sampling rate for synthesis (8khz or 16khz)" default:"16khz"`
	Format       string `long:"format" description:"Audio format for synthesis (wav or raw)" default:"wav"`
	Output       string `short:"o" long:"output" description:"Output file for synthesized audio" required:"true"`
}

type RecognizeCommand struct {
	url       string
	tokenFile string
	cmd       *RecognizeOpts
}

func NewRecognizeCommand(url, tokenFile string, cmd *RecognizeOpts) Command {
	return &RecognizeCommand{
		url:       url,
		tokenFile: tokenFile,
		cmd:       cmd,
	}
}

func (r *RecognizeCommand) Execute() error {
	recogniser, err := verbio_speech_center.NewRecogniser(r.url, r.tokenFile)
	log.Logger.Infof("Created recogniser")
	if err != nil {
		log.Logger.Fatalf("Error creating recogniser: %+v", err)
	}
	defer recogniser.Close()

	if r.cmd.Topic != "" {
		log.Logger.Warn("--topic is deprecated, use --topic-name instead")
	}

	var res string
	if r.cmd.Grammar != "" {
		if r.cmd.SpeechCompleteTimeout != 0 {
			log.Logger.Warn("Ignoring --speech-complete-timeout: grammar recognition is served by asr3, which exposes no endpointing setting")
		}
		res, err = recogniser.RecogniseWithGrammar(r.cmd.Audio, r.cmd.Grammar, r.cmd.Language, r.cmd.WordBoosting)
	} else if r.cmd.TopicName != "" || r.cmd.Topic != "" {
		res, err = recogniser.RecogniseWithTopic(r.cmd.Audio, verbio_speech_center.TopicRecognition{
			Topic:                 r.cmd.Topic,
			TopicName:             r.cmd.TopicName,
			Language:              r.cmd.Language,
			Provider:              r.cmd.Provider,
			WordBoosting:          r.cmd.WordBoosting,
			SpeechCompleteTimeout: r.cmd.SpeechCompleteTimeout,
		})
	} else {
		log.Logger.Fatal("Either a grammar or a topic name must be specified for recognition")
	}
	if err != nil {
		log.Logger.Fatalf("Error in recognition: %+v", err)
	}

	log.Logger.Infof("Result: %s", res)
	return nil
}

type SynthesizeCommand struct {
	url       string
	tokenFile string
	cmd       *SynthesizeOpts
}

func NewSynthesizeCommand(url, tokenFile string, cmd *SynthesizeOpts) Command {
	return &SynthesizeCommand{
		url:       url,
		tokenFile: tokenFile,
		cmd:       cmd,
	}
}

func parseFormat(format string) (ttsv1.AudioFormat, error) {
	switch format {
	case "wav":
		return ttsv1.AudioFormat_AUDIO_FORMAT_WAV_LPCM_S16LE, nil
	case "raw":
		return ttsv1.AudioFormat_AUDIO_FORMAT_RAW_LPCM_S16LE, nil
	default:
		return ttsv1.AudioFormat_AUDIO_FORMAT_WAV_LPCM_S16LE, fmt.Errorf("invalid format: %s (must be wav or raw)", format)
	}
}

func parseSamplingRate(rate string) (ttsv1.VoiceSamplingRate, error) {
	switch rate {
	case "8khz", "8kHz", "8":
		return ttsv1.VoiceSamplingRate_VOICE_SAMPLING_RATE_8KHZ, nil
	case "16khz", "16kHz", "16":
		return ttsv1.VoiceSamplingRate_VOICE_SAMPLING_RATE_16KHZ, nil
	default:
		return ttsv1.VoiceSamplingRate_VOICE_SAMPLING_RATE_8KHZ, fmt.Errorf("invalid sampling rate: %s (must be 8khz or 16khz)", rate)
	}
}

func (s *SynthesizeCommand) Execute() error {
	synthesizer, err := verbio_speech_center.NewSynthesizer(s.url, s.tokenFile)
	log.Logger.Infof("Created synthesizer")
	if err != nil {
		log.Logger.Fatalf("Error creating synthesizer: %+v", err)
	}
	defer func() {
		if err := synthesizer.Close(); err != nil {
			log.Logger.Errorf("Error closing synthesizer: %+v", err)
		}
	}()

	samplingRate, err := parseSamplingRate(s.cmd.SamplingRate)
	if err != nil {
		log.Logger.Fatalf("%v", err)
	}

	format, err := parseFormat(s.cmd.Format)
	if err != nil {
		log.Logger.Fatalf("%v", err)
	}

	err = synthesizer.StreamingSynthesizeSpeech(s.cmd.Text, s.cmd.Voice, samplingRate, format, s.cmd.Output)
	if err != nil {
		log.Logger.Fatalf("Error in synthesis: %+v", err)
	}

	log.Logger.Infof("Successfully synthesized speech to %s", s.cmd.Output)
	return nil
}

var globalOpts GlobalOpts
var parser = flags.NewParser(&globalOpts, flags.Default)

func main() {
	recognizeCmd := RecognizeOpts{}
	_, err := parser.AddCommand("recognize", "Recognize speech from audio file", "Recognize speech from an audio file using grammar or topic", &recognizeCmd)
	if err != nil {
		log.Logger.Fatalf("Failed to add 'recognize' command: %+v", err)
	}

	synthesizeCmd := SynthesizeOpts{}
	_, err = parser.AddCommand("synthesize", "Synthesize speech from text", "Synthesize speech from text to audio file", &synthesizeCmd)
	if err != nil {
		log.Logger.Fatalf("Failed to add 'synthesize' command: %+v", err)
	}

	_, err = parser.Parse()
	if err != nil {
		if flagsErr, ok := err.(*flags.Error); ok {
			if flagsErr.Type == flags.ErrUnknownCommand {
				return
			}
		}
		log.Logger.Fatalf("Invalid usage: %+v", err)
	}

	if parser.Active == nil {
		parser.WriteHelp(nil)
		log.Logger.Fatal("No command specified. Use 'recognize' or 'synthesize'")
	}

	if globalOpts.TokenFile == "" {
		log.Logger.Fatal("Token file is required. Use -t or --token-file")
	}

	commandName := parser.Active.Name

	log.InitLogger(globalOpts.LogLevel)
	log.Logger.Infof("Starting %s (%s)", constants.APP_NAME, constants.VERSION)

	url := BACKEND_URL
	if globalOpts.Url != "" {
		url = globalOpts.Url
	}

	log.Logger.Infof("Using the URL: [%s]", url)

	var command Command
	switch commandName {
	case "recognize":
		command = NewRecognizeCommand(url, globalOpts.TokenFile, &recognizeCmd)
	case "synthesize":
		command = NewSynthesizeCommand(url, globalOpts.TokenFile, &synthesizeCmd)
	default:
		log.Logger.Fatalf("Unknown command: %s", commandName)
	}

	if err := command.Execute(); err != nil {
		log.Logger.Fatalf("Command execution failed: %+v", err)
	}
}
