package itn_orchestrator

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"itn_json_types"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	logging "github.com/ipfs/go-log/v2"
	"go.uber.org/zap/zapcore"
)

var actions map[string]Action

func addAction(actions map[string]Action, action Action) {
	actions[action.Name()] = action
}

func init() {
	actions = map[string]Action{}
	addAction(actions, DiscoveryAction{})
	addAction(actions, PaymentsAction{})
	addAction(actions, KeyloaderAction{})
	addAction(actions, StopAction{})
	addAction(actions, WaitAction{})
	addAction(actions, FundAction{})
	addAction(actions, ZkappCommandsAction{})
	addAction(actions, SlotsWonAction{})
	addAction(actions, ResetGatingAction{})
	addAction(actions, IsolateAction{})
	addAction(actions, AllocateSlotsAction{})
	addAction(actions, RestartAction{})
	addAction(actions, JoinAction{})
	addAction(actions, SampleAction{})
	addAction(actions, ExceptAction{})
	addAction(actions, StopDaemonAction{})
	addAction(actions, RotateAction{})
	addAction(actions, SetZkappSoftLimitAction{})
	addAction(actions, SlotsCoveredCheckAction{})
}

type AwsConfig struct {
	Region    string `json:"region"`
	AccountId string `json:"account_id"`
	Prefix    string `json:"prefix"`
}

type AwsCredentials struct {
	AccessKeyId     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

func loadAwsCredentials(filename string, log logging.EventLogger) {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Error loading credentials file: %s", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var credentials AwsCredentials
	err = decoder.Decode(&credentials)
	if err != nil {
		log.Fatalf("Error loading credentials file: %s", err)
	}
	os.Setenv("AWS_ACCESS_KEY_ID", credentials.AccessKeyId)
	os.Setenv("AWS_SECRET_ACCESS_KEY", credentials.SecretAccessKey)
}

type OrchestratorConfig struct {
	LogLevel         zapcore.Level `json:",omitempty"`
	LogFile          string        `json:",omitempty"`
	Key              itn_json_types.Ed25519Privkey
	Aws              *AwsConfig `json:"aws,omitempty"`
	OnlineURL        string     `json:"onlineURL,omitempty"`
	FundDaemonPorts  []string   `json:",omitempty"`
	MinaExec         string     `json:",omitempty"`
	SlotDurationMs   int
	GenesisTimestamp itn_json_types.Time
	ControlExec      string   `json:",omitempty"`
	UrlOverrides     []string `json:",omitempty"`
	PrintRequests    bool     `json:"printRequests,omitempty"`
}

func (config *AwsConfig) GetBucketName() string {
	return config.AccountId + "-block-producers-uptime"
}

func LoadAppConfig(configFilename string) (res OrchestratorConfig) {
	configFile, err := os.Open(configFilename)
	if err != nil {
		os.Stderr.WriteString(fmt.Sprintf("failed to load config %s: %v", configFilename, err))
		os.Exit(2)
		return
	}
	decoder := json.NewDecoder(configFile)
	if err = decoder.Decode(&res); err != nil {
		os.Stderr.WriteString(fmt.Sprintf("failed to decode config %s: %v", configFilename, err))
		os.Exit(3)
		return
	}
	if (res.Aws == nil) == (res.OnlineURL == "") {
		os.Stderr.WriteString("Neither aws nor online url configured")
		os.Exit(11)
	}
	return
}

type CommandOrComment struct {
	command *Command
	comment string
}

func (v *CommandOrComment) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &v.comment); err == nil {
		return nil
	}
	cmd := Command{}
	if err := json.Unmarshal(data, &cmd); err != nil {
		return err
	}
	v.command = &cmd
	return nil
}

type OrchestratorError struct {
	Message string
	Code    int
}

func (e *OrchestratorError) Error() string {
	return e.Message
}

type outCacheT = map[string]map[int]map[string]OutputCacheEntry

func outputF(outCache outCacheT, log logging.StandardLogger, step int) func(string, any, bool, bool) error {
	return func(name string, value_ any, multiple bool, sensitive bool) error {
		value, err := json.Marshal(value_)
		if err != nil {
			return &OrchestratorError{
				Message: fmt.Sprintf("Error marshalling value %s for step %d: %v", name, step, err),
				Code:    7,
			}
		}
		if _, has := outCache[""][step]; !has {
			outCache[""][step] = map[string]OutputCacheEntry{}
		}
		prev, has := outCache[""][step][name]
		if has {
			if multiple && prev.Multi {
				outCache[""][step][name] = OutputCacheEntry{Multi: true, Values: append(prev.Values, value)}
			} else {
				return &OrchestratorError{
					Message: fmt.Sprintf("Error outputting multiple values for %s on step %d", name, step),
					Code:    8,
				}
			}
		} else {
			outCache[""][step][name] = OutputCacheEntry{Multi: multiple, Values: []json.RawMessage{value}}
		}
		if !sensitive {
			json, err := json.Marshal(Output{
				Name:  name,
				Multi: multiple,
				Value: value,
				Step:  step,
				Time:  time.Now().UTC(),
			})
			if err != nil {
				return &OrchestratorError{
					Message: fmt.Sprintf("Error marshalling output %s for step %d: %v", name, step, err),
					Code:    8,
				}
			}
			_, err = os.Stdout.Write(append(json, '\n'))
			if err != nil {
				return &OrchestratorError{
					Message: fmt.Sprintf("Error writing output %s for step %d: %v", name, step, err),
					Code:    8,
				}
			}
		}
		return nil
	}
}

func (awsConf *AwsConfig) load(ctx context.Context, log logging.StandardLogger) *AwsContext {
	awsCredentialsFile := os.Getenv("AWS_CREDENTIALS_FILE")
	if awsCredentialsFile != "" {
		loadAwsCredentials(awsCredentialsFile, log)
	}
	awsRuntimeConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsConf.Region))
	if err != nil {
		log.Fatalf("Error loading AWS configuration: %v", err)
	}
	client := s3.NewFromConfig(awsRuntimeConfig)
	return &AwsContext{Client: client, BucketName: aws.String(awsConf.GetBucketName()), Prefix: awsConf.Prefix}
}

func SetupConfig(ctx context.Context, orchestratorConfig OrchestratorConfig, log logging.StandardLogger) Config {
	nodeData := make(map[NodeAddress]NodeEntry)
	var awsctx *AwsContext
	if orchestratorConfig.Aws != nil {
		awsctx = orchestratorConfig.Aws.load(ctx, log)
	}

	if orchestratorConfig.Aws != nil {
		awsctx = orchestratorConfig.Aws.load(ctx, log)
	}

	config := Config{
		Ctx:              ctx,
		AwsContext:       awsctx,
		Sk:               ed25519.PrivateKey(orchestratorConfig.Key),
		Log:              log,
		FundDaemonPorts:  orchestratorConfig.FundDaemonPorts,
		MinaExec:         orchestratorConfig.MinaExec,
		NodeData:         nodeData,
		SlotDurationMs:   orchestratorConfig.SlotDurationMs,
		GenesisTimestamp: time.Time(orchestratorConfig.GenesisTimestamp),
		ControlExec:      orchestratorConfig.ControlExec,
		OnlineURL:        orchestratorConfig.OnlineURL,
		UrlOverrides:     orchestratorConfig.UrlOverrides,
		PrintRequests:    orchestratorConfig.PrintRequests,
	}
	if config.MinaExec == "" {
		config.MinaExec = "mina"
	}
	if config.StopDaemonDelaySec == 0 {
		config.StopDaemonDelaySec = 10
	}
	return config
}

func EmptyOutputCache() outCacheT {
	return map[string]map[int]map[string]OutputCacheEntry{
		"": {},
	}
}

func RunActions(inDecoder *json.Decoder, config Config, outCache outCacheT, log logging.StandardLogger, step int,
	handlePrevAction func() error, actionAccum *[]ActionIO, rconfig ResolutionConfig, prevAction *BatchAction) error {
	for {

		select {
		case <-config.Ctx.Done():
			log.Infof("Experiment canceled")
			return nil
		default:
		}

		var commandOrComment CommandOrComment
		if err := inDecoder.Decode(&commandOrComment); err != nil {
			if err != io.EOF {
				return &OrchestratorError{
					Message: fmt.Sprintf("Error decoding command for step %d: %v", step, err),
					Code:    5,
				}
			}
			break
		}
		if commandOrComment.command == nil {
			log.Info(commandOrComment.comment)
			fmt.Fprintln(os.Stderr, commandOrComment.comment)
			continue
		}
		cmd := *commandOrComment.command
		if *prevAction != nil && (*prevAction).Name() != cmd.Action {
			handlePrevAction()
		}
		params, err := ResolveParams(rconfig, step, cmd.Params)
		if err != nil {
			return &OrchestratorError{
				Message: fmt.Sprintf("Error resolving params for step %d: %v", step, err),
				Code:    6,
			}
		}
		action := actions[cmd.Action]
		if action == nil {
			return &OrchestratorError{
				Message: fmt.Sprintf("Unknown action name: %s", cmd.Action),
				Code:    10,
			}
		}
		batchAction, isBatchAction := action.(BatchAction)

		if isBatchAction {
			if err := batchAction.Validate(params); err != nil {
				return &OrchestratorError{
					Message: fmt.Sprintf("Error validating action '%s' for step %d: %v", cmd.Action, step, err),
					Code:    1,
				}
			}
			*prevAction = batchAction
			*actionAccum = append(*actionAccum, ActionIO{
				Params: params,
				Output: outputF(outCache, log, step),
			})
		} else {
			log.Infof("Performing step %s (%d)", cmd.Action, step)
			log.Debugf("Cache: %s, Params: %s", outCache, string(params))
			err = action.Run(config, params, outputF(outCache, log, step))
			if err != nil {
				return &OrchestratorError{
					Message: fmt.Sprintf("Error running step %d: %v", step, err),
					Code:    9,
				}
			}
		}
		step++
	}
	return nil
}
