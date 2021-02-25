package bot

import (
	"errors"
	"fmt"
	"github.com/drewnorman/jt-slackbot/event"
	"github.com/drewnorman/jt-slackbot/http"
	"github.com/drewnorman/jt-slackbot/websocket"
	"os"
	"os/signal"
	"time"
)

type Bot struct {
	apiUrl             string
	appToken           string
	botToken           string
	maxConnectAttempts int
	debugWssReconnects bool
	httpClient         *http.Client
	wsClient           *websocket.Client
	handler            *event.Handler
	interrupt          chan os.Signal
}

type Parameters struct {
	ApiUrl             string
	AppToken           string
	BotToken           string
	MaxConnectAttempts int
	DebugWssReconnects bool
}

func New(params *Parameters) (*Bot, error) {
	if params.ApiUrl == "" {
		return nil, errors.New("missing api url")
	}
	if params.AppToken == "" {
		return nil, errors.New("missing app token")
	}
	if params.BotToken == "" {
		return nil, errors.New("missing bot token")
	}

	maxConnectAttempts := 3
	debugWssReconnects := false
	if params.MaxConnectAttempts != maxConnectAttempts {
		maxConnectAttempts = params.MaxConnectAttempts
	}
	if params.DebugWssReconnects {
		debugWssReconnects = true
	}

	bot := &Bot{
		apiUrl:             params.ApiUrl,
		appToken:           params.AppToken,
		botToken:           params.BotToken,
		maxConnectAttempts: maxConnectAttempts,
		debugWssReconnects: debugWssReconnects,
	}

	httpClient, err := http.NewClient(&http.ClientParameters{
		ApiUrl:   bot.apiUrl,
		AppToken: bot.appToken,
		BotToken: bot.botToken,
	})
	if err != nil {
		return nil, err
	}
	bot.httpClient = httpClient

	bot.interrupt = make(chan os.Signal, 1)
	signal.Notify(bot.interrupt, os.Interrupt)

	return bot, nil
}

func (bot *Bot) AttemptToConnect() error {
	wssUrl := ""
	attemptsLeft := bot.maxConnectAttempts
	for attemptsLeft > 0 {
		var err error
		wssUrl, err = bot.httpClient.RequestWssUrl(bot.debugWssReconnects)
		if err != nil {
			attemptsLeft -= 1
			continue
		}
		break
	}
	if wssUrl == "" {
		return fmt.Errorf(
			"failed to retrieve slack wss url after %d attempts",
			bot.maxConnectAttempts,
		)
	}

	bot.wsClient = websocket.NewClient()
	attemptsLeft = bot.maxConnectAttempts
	for attemptsLeft > 0 {
		err := bot.wsClient.Connect(wssUrl)
		if err != nil {
			attemptsLeft -= 1
			if attemptsLeft == 0 {
				return fmt.Errorf(
					"failed to connect to slack wss after %d attempts",
					bot.maxConnectAttempts,
				)
			}
			continue
		}
		break
	}

	return nil
}

func (bot *Bot) PrepareWorkspace() error {
	channels, err := bot.httpClient.PublicChannels()
	if err != nil {
		return err
	}

	for _, channel := range channels {
		channelId, ok := channel.(map[string]interface{})["id"].(string)
		if !ok {
			continue
		}
		err = bot.httpClient.JoinChannel(channelId)
		if err != nil {
			continue
		}
	}

	if err != nil {
		return err
	}

	return nil
}

func (bot *Bot) Start() (bool, error) {
	var err error
	bot.handler, err = event.NewHandler(&event.HandlerParameters{
		HttpClient: bot.httpClient,
	})
	if err != nil {
		return false, err
	}

	events := make(chan map[string]interface{})
	complete := make(chan struct{})

	go bot.wsClient.Listen(events)
	go bot.handler.Process(events, complete)

	restart := true
	for {
		select {
		case <-complete:
			break
		case <-bot.interrupt:
			restart = false
			break
		default:
			continue
		}
		break
	}

	_, err = bot.wsClient.Close(complete, 1*time.Second)
	if err != nil {
		return false, err
	}
	return restart, bot.wsClient.Disconnect()
}
