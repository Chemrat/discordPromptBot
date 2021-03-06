package main

import (
	"encoding/json"
	"errors"
	"github.com/PuerkitoBio/goquery"
	"github.com/bwmarrin/discordgo"
	"io/ioutil"
	"log"
	"math/rand"
	"strings"
	"time"
)

var authFile = "auth.json"

var stopPromptThread = make(chan bool)
var stopBot = make(chan bool)

var ptypes = make([]string, 0)

var LastManualPrompt time.Time

type AuthTokens struct {
	ID    string `json:"ID"`
	Token string `json:"Token"`
}

type WorkerStatus struct {
	IsRunning  bool          `json:"IsRunning"`
	Period     time.Duration `json:"Period"`
	LastPrompt time.Time     `json:"LastPrompt"`
	ChannelID  string        `json:"ChannelID"`
}

var auth AuthTokens
var status WorkerStatus

func init() {
	file, err := ioutil.ReadFile(authFile)
	if err == nil {
		err = json.Unmarshal(file, &auth)
		if err != nil {
			log.Fatal(err)
		}
		log.Println("Auth tokens loaded")
	} else {
		log.Println("Failed to load auth tokens: ", err)
	}

	ptypes = append(ptypes,
		"character",
		"creature")

	rand.Seed(time.Now().Unix())
}

func main() {
	RestoreACL()
	RestorePrompts()

	discordSession, err := discordgo.New("Bot " + auth.Token)
	if err != nil {
		log.Fatal("Failed to create a Discord session:", err)
	}

	discordSession.ShouldReconnectOnError = true
	discordSession.AddHandler(onConnect)
	discordSession.AddHandler(onDisconnect)
	discordSession.AddHandler(onMessageCreated)

	err = discordSession.Open()
	if err != nil {
		log.Fatal("Error opening connection: ", err)
	}

	RestoreWorkerStatus(discordSession)

	<-stopBot
	return
}

func onConnect(s *discordgo.Session, c *discordgo.Connect) {
	// this event is CONSTANTLY triggered by discordgo, I don't know why
	// log.Println("Connected")
}

func onDisconnect(s *discordgo.Session, m *discordgo.Connect) {
	// log.Println("Disconnected")
}

func onMessageCreated(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == auth.ID {
		return
	}

	// reduce the log noise
	// log.Printf("%20s %20s %20s > %s\n", m.ChannelID, m.Author.ID, m.Author.Username, m.Content)

	c, err := s.State.Channel(m.ChannelID)
	if err != nil {
		log.Println("Error getting channel: ", err)
		return
	}

	switch {
	case strings.HasPrefix(m.Content, "!start"):
		if c.Type != discordgo.ChannelTypeGuildText {
			SafeMessage(s, c, "Use this command in a non-private channel")
			return
		}

		stopWorker()
		params := strings.SplitN(m.Content, " ", 2)
		if len(params) == 2 {
			duration, err := time.ParseDuration(params[1])
			if err != nil {
				SafeMessage(s, c, "Sorry, I do not recognize this duration! Duration should look like 1h10m or 24h")
				return
			}

			minimumPeriod := 15 * time.Minute
			if duration < minimumPeriod {
				SafeMessage(s, c, "Duration is too short. Minimum: "+minimumPeriod.String())
				return
			}

			go worker(s, c, duration, 0)
		} else {
			go worker(s, c, 24*time.Hour, 0)
		}
	case m.Content == "!stop":
		stopWorker()
	case m.Content == "!help":
		const HelpMessage = `Commands:
		!add some prompt text - add a prompt
		!remove some prompt text - remove that prompt (only author can delete their prompts)
		!list - list existing prompts
		!prompt - get a prompt right now
		!start [period] - start posting prompts every [period] (24h by default) on current channel
		!stop - stop posting prompts
		!get [type] - pull random prompt from internet (types: `

		const AdminHelpMessage = `Service commands:
		!purge - delete all prompts
		!remove some prompt text - remove any prompt
		!promote userId - add user to service ACL (requires Discord user ID; use !myid)
		!die - terminate bot process`

		SafeMessage(s, c, HelpMessage+strings.Join(ptypes[:], " ")+"), defaults to random")
		if isAdmin(m.Author.ID) {
			SafeMessage(s, c, AdminHelpMessage)
		}
	case m.Content == "!prompt":
		prompt, err := PopPrompt(true)
		if err != nil {
			SafeMessage(s, c, "<@"+m.Author.ID+">, "+err.Error())
		} else {
			SafeMessage(s, c, "<@"+m.Author.ID+">, prompt for you: "+prompt.Text+" (added by "+prompt.Author+")")
		}

	case strings.HasPrefix(m.Content, "!add"):
		params := strings.SplitN(m.Content, " ", 2)
		if len(params) != 2 {
			SafeMessage(s, c, "Usage: !add prompt_text")
			return
		}

		err = PushPrompt(params[1], m.Author.Username, m.Author.ID)
		if err != nil {
			SafeMessage(s, c, "Uh oh, "+err.Error())
		} else {
			SafeMessage(s, c, "Prompt added")
		}

	case strings.HasPrefix(m.Content, "!remove"):
		params := strings.SplitN(m.Content, " ", 2)
		if len(params) != 2 {
			SafeMessage(s, c, "Usage: !remove prompt_text")
			return
		}

		err = DeletePrompt(params[1], m.Author.ID)
		if err != nil {
			SafeMessage(s, c, "Uh oh, "+err.Error())
		} else {
			SafeMessage(s, c, "Prompt deleted")
		}

	case m.Content == "!purge":
		err = PurgePrompts(m.Author.ID)

		if err != nil {
			SafeMessage(s, c, "Uh oh, "+err.Error())
		} else {
			SafeMessage(s, c, "Prompts purged")
		}

	case m.Content == "!list":
		if len(prompts) == 0 {
			SafeMessage(s, c, "No prompts!")
			return
		}

		allPromptsSerialized := "Prompt list:\n"
		for _, p := range prompts {
			allPromptsSerialized += "	" + p.Text + " (by " + p.Author + ")\n"
		}
		SafeMessage(s, c, allPromptsSerialized)

	case m.Content == "!die":
		if isAdmin(m.Author.ID) {
			SafeMessage(s, c, "RIP")
			stopBot <- true
		} else {
			SafeMessage(s, c, "Not allowed. Your user ID is not on service ACL.")
		}

	case m.Content == "!myid":
		SafeMessage(s, c, "<@"+m.Author.ID+"> Your ID is "+m.Author.ID)

	case strings.HasPrefix(m.Content, "!promote"):
		params := strings.SplitN(m.Content, " ", 2)
		if len(params) != 2 {
			SafeMessage(s, c, "Usage: !promote userID")
			return
		}

		err = AddToACL(params[1], m.Author.ID)
		if err != nil {
			SafeMessage(s, c, "Uh oh, "+err.Error())
		} else {
			SafeMessage(s, c, "User added to service ACL")
		}

	case strings.HasPrefix(m.Content, "!get"):
		params := strings.SplitN(m.Content, " ", 2)

		if len(params) == 2 {
			SafeMessage(s, c, PullPrompt(params[1]))
			return
		}

		SafeMessage(s, c, PullRandomPrompt())
	}
}

func worker(s *discordgo.Session, c *discordgo.Channel, duration time.Duration, pause time.Duration) {
	log.Println("Starting worker thread with duration " + duration.String())
	status.ChannelID = c.ID
	status.Period = duration

	if pause <= 0 {
		prompt, err := PopPrompt(true)

		if err != nil {
			SafeMessage(s, c, "No more prompts, stopping now")
			log.Println("Can't get prompt: ", err)
			SaveWorkerStatus(false)
			return
		}

		SafeMessage(s, c, "@everyone, new prompt: "+prompt.Text+" (added by "+prompt.Author+")\nNext prompt in "+duration.String())
		status.LastPrompt = time.Now()
	} else {
		if workerCycle(s, c, pause) {
			SaveWorkerStatus(false)
			return
		}
	}

	SaveWorkerStatus(true)
	for !workerCycle(s, c, duration) {
		SaveWorkerStatus(true)
	}

	SaveWorkerStatus(false)
}

func workerCycle(s *discordgo.Session, c *discordgo.Channel, cycleLength time.Duration) (quit bool) {
	select {
	case <-stopPromptThread:
		SafeMessage(s, c, "Stopped")
		return true
	case <-time.After(cycleLength):
		prompt, err := PopPrompt(true)
		if err != nil {
			SafeMessage(s, c, "No more prompts, stopping now")
			log.Println("Can't get prompt: ", err)
			return true
		}

		SafeMessage(s, c, "@everyone, new prompt: "+prompt.Text+" (added by "+prompt.Author+")")
		status.LastPrompt = time.Now()
		return false
	}
}

func stopWorker() {
	select {
	case stopPromptThread <- true:
		log.Println("Thread stopped")
	default:
		log.Println("Thread wasn't running")
	}
}

func SafeMessage(s *discordgo.Session, c *discordgo.Channel, msg string) {
	_, err := s.ChannelMessageSend(c.ID, msg)

	log.Println(">>> " + msg)
	if err != nil {
		log.Println("Error sending message: ", err)
	}
}

func RestoreWorkerStatus(s *discordgo.Session) {
	file, err := ioutil.ReadFile("status.json")

	if err != nil {
		log.Println("Can't restore status: ", err)
		return
	}

	err = json.Unmarshal(file, &status)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Restored status: ", status)

	if !status.IsRunning {
		return
	}

	until := time.Until(status.LastPrompt.Add(status.Period))

	log.Println("Until next prompt: " + until.String())

	c, err := s.Channel(status.ChannelID)
	if err != nil {
		log.Println("Channel ID " + status.ChannelID + " is no longer valid")
		return
	}

	go worker(s, c, status.Period, until)
}

func SaveWorkerStatus(isRunning bool) {
	status.IsRunning = isRunning

	WriteWorkerStatus()
}

func WriteWorkerStatus() {
	exportedJSON, err := json.Marshal(status)
	if err != nil {
		err = errors.New("Failed to serialize list: " + err.Error())
		return
	}

	err = ioutil.WriteFile("status.json", exportedJSON, 0644)

	if err != nil {
		err = errors.New("Failed to write status file: " + err.Error())
	}
}

func PullPrompt(ptype string) string {
	if LastManualPrompt.Add(time.Minute * 10).After(time.Now()) {
		return "On cooldown"
	}

	switch ptype {
	case "character":
		return pullArtprompts(ptype)
	case "creature":
		return pullArtprompts(ptype)
	default:
		return "I know only these prompt types: " + strings.Join(ptypes[:], " ")
	}
}

func PullRandomPrompt() string {
	return PullPrompt(ptypes[rand.Intn(len(ptypes))])
}

func pullArtprompts(ptype string) string {
	url := ""
	switch ptype {
	case "character":
		url = "http://artprompts.org/character/"
	case "creature":
		url = "http://artprompts.org/creature-prompts/"
	default:
		return "No idea"
	}

	doc, err := goquery.NewDocument(url)
	if err != nil {
		log.Println("Error getting prompt: ", err)
		return "Failed to pull page :<"
	}

	result := ""
	doc.Find(".prompttextdiv").Each(func(i int, s *goquery.Selection) {
		result = result + s.Text()
	})

	if len(result) == 0 {
		return "Failed to parse page :<"
	} else {
		LastManualPrompt = time.Now()
		return "From artprompts.org, type " + ptype + ":\n```" + result + "```"
	}
}
