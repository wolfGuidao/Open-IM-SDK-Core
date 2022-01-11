package common

import (
	"errors"
	"open_im_sdk/pkg/constant"
	"open_im_sdk/pkg/log"
	"open_im_sdk/pkg/utils"
	"time"
)

func TriggerCmdNewMsgCome(msg utils.ArrMsg, conversationCh chan Cmd2Value) error {
	c2v := Cmd2Value{Cmd: constant.CmdNewMsgCome, Value: msg}
	return sendCmd(conversationCh, c2v, 1)
}

func TriggerCmdLogout(msg utils.ArrMsg, conversationCh chan Cmd2Value) error {
	c2v := Cmd2Value{Cmd: constant.CmdLogout, Value: msg}
	return sendCmd(conversationCh, c2v, 1)
}


func TriggerCmdDeleteConversationAndMessage(sourceID, conversationID string, sessionType int, conversationCh chan Cmd2Value) error {
	c2v := Cmd2Value{
		Cmd:   constant.CmdDeleteConversation,
		Value: DeleteConNode{SourceID: sourceID, ConversationID: conversationID, SessionType: sessionType},
	}

	return sendCmd(conversationCh, c2v, 1)
}
func TriggerCmdUpdateConversation(node UpdateConNode, conversationCh chan Cmd2Value) error {
	c2v := Cmd2Value{
		Cmd:   constant.CmdUpdateConversation,
		Value: node,
	}

	return sendCmd(conversationCh, c2v, 1)
}

type DeleteConNode struct {
	SourceID       string
	ConversationID string
	SessionType    int
}
type UpdateConNode struct {
	ConId  string
	Action int //1 Delete the conversation; 2 Update the latest news in the conversation or add a conversation; 3 Put a conversation on the top;
	// 4 Cancel a conversation on the top, 5 Messages are not read and set to 0, 6 New conversations
	Args interface{}
}

type Cmd2Value struct {
	Cmd   string
	Value interface{}
}

func unInitAll(conversationCh chan Cmd2Value) {
	c2v := Cmd2Value{Cmd: constant.CmdUnInit}
	_ = sendCmd(conversationCh, c2v, 1)
}

type goroutine interface {
	Work(cmd Cmd2Value)
	GetCh() chan Cmd2Value
}

func DoListener(Li goroutine) {
	log.Info("internal", "doListener start.", Li.GetCh())
	for {
		log.Info("doListener for.")
		select {
		case cmd := <-Li.GetCh():
			if cmd.Cmd == constant.CmdUnInit {
				log.Info("doListener goroutine.")
				return
			}
			log.Info("doListener work.")
			Li.Work(cmd)
		}
	}
}

func sendCmd(ch chan Cmd2Value, value Cmd2Value, timeout int64) error {
	var flag = 0
	select {
	case ch <- value:
		flag = 1
	case <-time.After(time.Second * time.Duration(timeout)):
		flag = 2
	}
	if flag == 1 {
		return nil
	} else {
		return errors.New("send cmd timeout")
	}
}
