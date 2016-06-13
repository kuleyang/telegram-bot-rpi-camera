// telegram bot for using raspberry pi camera module
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	bot "github.com/meinside/telegram-bot-go"

	"github.com/meinside/telegram-bot-rpi-camera/conf"
	"github.com/meinside/telegram-bot-rpi-camera/helper"
)

type Status int16

const (
	StatusWaiting Status = iota
)

const (
	TempDir = "/var/tmp"

	MinImageWidth  = 400
	MinImageHeight = 300
)

type Session struct {
	UserId        string
	CurrentStatus Status
}

type SessionPool struct {
	Sessions map[string]Session
	sync.Mutex
}

// variables
var apiToken string
var monitorInterval int
var isVerbose bool
var availableIds []string
var imageWidth, imageHeight int
var pool SessionPool
var launched time.Time

// keyboards
var allKeyboards = [][]bot.KeyboardButton{
	bot.NewKeyboardButtons(conf.CommandCapture),
	bot.NewKeyboardButtons(conf.CommandStatus, conf.CommandHelp),
}
var cancelKeyboard = [][]bot.KeyboardButton{
	bot.NewKeyboardButtons(conf.CommandCancel),
}

// initialization
func init() {
	launched = time.Now()

	// read variables from config file
	if config, err := helper.GetConfig(); err == nil {
		apiToken = config.ApiToken
		availableIds = config.AvailableIds
		monitorInterval = config.MonitorInterval
		if monitorInterval <= 0 {
			monitorInterval = conf.DefaultMonitorIntervalSeconds
		}
		isVerbose = config.IsVerbose
		imageWidth = config.ImageWidth
		if imageWidth < MinImageWidth {
			imageWidth = MinImageWidth
		}
		imageHeight = config.ImageHeight
		if imageHeight < MinImageHeight {
			imageHeight = MinImageHeight
		}

		// initialize variables
		sessions := make(map[string]Session)
		for _, v := range availableIds {
			sessions[v] = Session{
				UserId:        v,
				CurrentStatus: StatusWaiting,
			}
		}
		pool = SessionPool{
			Sessions: sessions,
		}
	} else {
		panic(err.Error())
	}
}

// check if given Telegram id is available
func isAvailableId(id string) bool {
	for _, v := range availableIds {
		if v == id {
			return true
		}
	}
	return false
}

// for showing help message
func getHelp() string {
	return `
Following commands are supported:

*For Raspberry Pi Camera Module*

/capture : capture an still image with *raspistill*

*Others*

/status : show this bot's status
/help : show this help message
`
}

// for showing current status of this bot
func getStatus() string {
	return fmt.Sprintf("Uptime: %s\nMemory Usage: %s", helper.GetUptime(launched), helper.GetMemoryUsage())
}

// process incoming update from Telegram
func processUpdate(b *bot.Bot, update bot.Update) bool {
	// check username
	var userId string
	if update.Message.From.Username == nil {
		log.Printf("*** Not allowed (no user name): %s\n", *update.Message.From.FirstName)
		return false
	}
	userId = *update.Message.From.Username
	if !isAvailableId(userId) {
		log.Printf("*** Id not allowed: %s\n", userId)
		return false
	}

	// process result
	result := false

	pool.Lock()
	if session, exists := pool.Sessions[userId]; exists {
		// text from message
		var txt string
		if update.Message.HasText() {
			txt = *update.Message.Text
		} else {
			txt = ""
		}

		var message string
		var options map[string]interface{} = map[string]interface{}{
			"reply_markup": bot.ReplyKeyboardMarkup{
				Keyboard:       allKeyboards,
				ResizeKeyboard: true,
			},
			"parse_mode": bot.ParseModeMarkdown,
		}

		switch session.CurrentStatus {
		case StatusWaiting:
			switch {
			// start
			case strings.HasPrefix(txt, conf.CommandStart):
				message = conf.MessageDefault
			// capture
			case strings.HasPrefix(txt, conf.CommandCapture):
				message = ""
			// status
			case strings.HasPrefix(txt, conf.CommandStatus):
				message = getStatus()
			// help
			case strings.HasPrefix(txt, conf.CommandHelp):
				message = getHelp()
			// fallback
			default:
				message = fmt.Sprintf("*%s*: %s", txt, conf.MessageUnknownCommand)
			}
		}

		if len(message) > 0 {
			// send message
			if sent := b.SendMessage(update.Message.Chat.Id, &message, options); sent.Ok {
				result = true
			} else {
				log.Printf("*** Failed to send message: %s\n", *sent.Description)
			}
		} else {
			// 'typing...'
			b.SendChatAction(update.Message.Chat.Id, bot.ChatActionTyping)

			// send photo
			if filepath, err := helper.CaptureRaspiStill(TempDir, imageWidth, imageHeight); err == nil {
				// 'uploading photo...'
				b.SendChatAction(update.Message.Chat.Id, bot.ChatActionUploadPhoto)

				// send photo
				if sent := b.SendPhoto(update.Message.Chat.Id, &filepath, options); sent.Ok {
					if err := os.Remove(filepath); err != nil {
						log.Printf("*** Failed to delete temp file: %s\n", err)
					}
					result = true
				} else {
					log.Printf("*** Failed to send photo: %s\n", *sent.Description)
				}
			} else {
				log.Printf("*** Image capture failed: %s\n", err)
			}
		}
	} else {
		log.Printf("*** Session does not exist for id: %s\n", userId)
	}
	pool.Unlock()

	return result
}

func main() {
	client := bot.NewClient(apiToken)
	client.Verbose = isVerbose

	// get info about this bot
	if me := client.GetMe(); me.Ok {
		log.Printf("Launching bot: @%s (%s)\n", *me.Result.Username, *me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := client.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			client.StartMonitoringUpdates(0, monitorInterval, func(b *bot.Bot, update bot.Update, err error) {
				if err == nil {
					if update.Message != nil {
						processUpdate(b, update)
					}
				} else {
					log.Printf("*** Error while receiving update (%s)\n", err.Error())
				}
			})
		} else {
			panic("Failed to delete webhook")
		}
	} else {
		panic("Failed to get info of the bot")
	}
}