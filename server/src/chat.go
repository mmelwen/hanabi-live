// Chat-related subroutines

package main

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/Hanabi-Live/hanabi-live/logger"
)

const (
	// When sending the in-game chat history,
	// only send the last X messages to prevent clients from becoming overloaded
	// (in case someone maliciously spams a lot of messages)
	ChatLimit = 1000
)

var (
	// From https://github.com/discordjs/discord.js/blob/stable/src/structures/MessageMentions.js#L221
	mentionRegExp = regexp.MustCompile(`&lt;@!?(\d{17,19})&gt;`)
	roleRegExp    = regexp.MustCompile(`&lt;@&amp;(\d{17,19})&gt;`)
	channelRegExp = regexp.MustCompile(`&lt;#(\d{17,19})&gt;`)
	spoilerRegExp = regexp.MustCompile(`(?:^| )\|\|(.+?)\|\|(?: |$)`)
)

type ChatMessage struct {
	Msg       string    `json:"msg"`
	Who       string    `json:"who"`
	Discord   bool      `json:"discord"`
	Server    bool      `json:"server"`
	Datetime  time.Time `json:"datetime"`
	Room      string    `json:"room"`
	Recipient string    `json:"recipient"`
}

// chatServerSend is a helper function to send a message from the server
// (e.g. to give feedback to a user after they type a command,
// to notify that the server is shutting down, etc.)
func chatServerSend(ctx context.Context, msg string, room string, noTablesLock bool) {
	commandChat(ctx, nil, &CommandData{ // nolint: exhaustivestruct
		Msg:          msg,
		Room:         room,
		Server:       true,
		NoTableLock:  true,
		NoTablesLock: noTablesLock,
	})
}

// chatServerSendAll is a helper function to broadcast a message to everyone on the server,
// whether they are in the lobby or in the middle of a game
// It is assumed that the tables mutex is locked when calling this function
func chatServerSendAll(ctx context.Context, msg string) {
	chatServerSend(ctx, msg, "lobby", false)

	tableList := tables.GetList(true)
	roomNames := make([]string, 0)
	for _, t := range tableList {
		t.Lock(ctx)
		roomNames = append(roomNames, t.GetRoomName())
		t.Unlock(ctx)
	}

	for _, roomName := range roomNames {
		chatServerSend(ctx, msg, roomName, false)
	}
}

// chatServerSendPM is for sending non-public messages to specific users
func chatServerSendPM(s *Session, msg string, room string) {
	s.Emit("chat", &ChatMessage{
		Msg:       msg,
		Who:       WebsiteName,
		Discord:   false,
		Server:    true,
		Datetime:  time.Now(),
		Room:      room,
		Recipient: s.Username,
	})
}

func chatFillAll(msg string) string {
	if discord == nil {
		return msg
	}

	// Convert Discord mentions to users, channels and roles
	msg = chatFillMentions(msg)
	msg = chatFillRoles(msg)
	msg = chatFillChannels(msg)

	// Convert other Discord tags
	msg = chatReplaceSpoilers(msg)

	return msg
}

func chatFillMentions(msg string) string {
	if discord == nil {
		return msg
	}

	// Discord mentions are in the form of "<@12345678901234567>"
	// By the time the message gets here, it will be sanitized to "&lt;@12345678901234567&gt;"
	// They can also be in the form of "<@!12345678901234567>" (with a "!" after the "@")
	// if a nickname is set for that person
	// We want to convert this to the username,
	// so that the lobby displays messages in a manner similar to the Discord client
	for {
		match := mentionRegExp.FindStringSubmatch(msg)
		if match == nil || len(match) <= 1 {
			break
		}
		discordID := match[1]
		username := discordGetNickname(discordID)
		msg = strings.ReplaceAll(msg, "&lt;@"+discordID+"&gt;", "@"+username)
		msg = strings.ReplaceAll(msg, "&lt;@!"+discordID+"&gt;", "@"+username)
	}
	return msg
}

func chatFillRoles(msg string) string {
	if discord == nil {
		return msg
	}

	// Discord roles are in the form of "<@&12345678901234567>"
	// By the time the message gets here, it will be sanitized to "&lt;@&amp;12345678901234567&gt;"
	for {
		match := roleRegExp.FindStringSubmatch(msg)
		if match == nil || len(match) <= 1 {
			break
		}
		discordID := match[1]
		role := discordGetRole(discordID)
		msg = strings.ReplaceAll(msg, "&lt;@&amp;"+discordID+"&gt;", "@"+role)
	}
	return msg
}

func chatFillChannels(msg string) string {
	if discord == nil {
		return msg
	}

	// Discord channels are in the form of "<#380813128176500736>"
	// By the time the message gets here, it will be sanitized to "&lt;#380813128176500736&gt;"
	for {
		match := channelRegExp.FindStringSubmatch(msg)
		if match == nil || len(match) <= 1 {
			break
		}
		discordID := match[1]
		channel := discordGetChannel(discordID)
		msg = strings.ReplaceAll(msg, "&lt;#"+discordID+"&gt;", "#"+channel)
	}
	return msg
}

func chatReplaceSpoilers(msg string) string {
	if discord == nil {
		return msg
	}

	for {
		match := spoilerRegExp.FindAllStringSubmatch(msg, -1)
		if len(match) == 0 {
			break
		}

		for _, m := range match {
			text := m[1]
			msg = strings.ReplaceAll(msg, "||"+text+"||", "<span class=\"spoiler\">"+text+"</span>")
		}
	}
	return msg
}

type ChatListMessage struct {
	List   []*ChatMessage `json:"list"`
	Unread int            `json:"unread"`
}

func chatSendPastFromDatabase(s *Session, room string, count int) bool {
	var rawMsgs []DBChatMessage
	if v, err := models.ChatLog.Get(room, count); err != nil {
		logger.Error("Failed to get the lobby chat history for user \"" + s.Username + "\": " + err.Error())
		s.Error(DefaultErrorMsg)
		return false
	} else {
		rawMsgs = v
	}

	msgs := make([]*ChatMessage, 0)
	for i := len(rawMsgs) - 1; i >= 0; i-- {
		// The chat messages were queried from the database in order from newest to newest
		// We want to send them to the client in the reverse order so that
		// the newest messages display at the bottom
		rawMsg := rawMsgs[i]
		discord := false
		server := false
		if rawMsg.Name == "__server" {
			server = true
		}
		if rawMsg.DiscordName.Valid {
			server = false
			discord = true
			rawMsg.Name = rawMsg.DiscordName.String
		}
		rawMsg.Message = chatFillAll(rawMsg.Message)
		msg := &ChatMessage{
			Msg:       rawMsg.Message,
			Who:       rawMsg.Name,
			Discord:   discord,
			Server:    server,
			Datetime:  rawMsg.Datetime,
			Room:      room,
			Recipient: "",
		}
		msgs = append(msgs, msg)
	}
	s.Emit("chatList", &ChatListMessage{
		List:   msgs,
		Unread: 0,
	})

	return true
}

func chatSendPastFromTable(s *Session, t *Table) {
	chatList := make([]*ChatMessage, 0)
	i := 0
	if len(t.Chat) > ChatLimit {
		i = len(t.Chat) - ChatLimit
	}
	for ; i < len(t.Chat); i++ {
		// We have to convert the *GameChatMessage to a *ChatMessage
		gcm := t.Chat[i]
		cm := &ChatMessage{
			Msg:       gcm.Msg,
			Who:       gcm.Username,
			Discord:   false,
			Server:    gcm.Server,
			Datetime:  gcm.Datetime,
			Room:      t.GetRoomName(),
			Recipient: "",
		}
		chatList = append(chatList, cm)
	}
	s.Emit("chatList", &ChatListMessage{
		List:   chatList,
		Unread: len(t.Chat) - t.ChatRead[s.UserID],
	})
}
