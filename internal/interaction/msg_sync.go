// Copyright © 2023 OpenIM SDK. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package interaction

import (
	"context"
	"open_im_sdk/pkg/common"
	"open_im_sdk/pkg/constant"
	"open_im_sdk/pkg/db/db_interface"
	"open_im_sdk/sdk_struct"

	"github.com/OpenIMSDK/Open-IM-Server/pkg/common/log"
	"github.com/OpenIMSDK/Open-IM-Server/pkg/proto/sdkws"
)

const (
	connectPullNums = 1
	defaultPullNums = 10
)

// The callback synchronization starts. The reconnection ends
type MsgSyncer struct {
	loginUserID        string                // login user ID
	longConnMgr        *LongConnMgr          // long connection manager
	PushMsgAndMaxSeqCh chan common.Cmd2Value // channel for receiving push messages and the maximum SEQ number
	conversationCh     chan common.Cmd2Value // storage and session triggering
	syncedMaxSeqs      map[string]int64      // map of the maximum synced SEQ numbers for all group IDs
	db                 db_interface.DataBase // data store
	syncTimes          int                   // times of sync
	ctx                context.Context       // context
}

func (m *MsgSyncer) Work(cmd common.Cmd2Value) {
	//TODO implement me
	panic("implement me")
}

func (m *MsgSyncer) GetCh() chan common.Cmd2Value {
	//TODO implement me
	panic("implement me")
}

// NewMsgSyncer creates a new instance of the message synchronizer.
func NewMsgSyncer(ctx context.Context, conversationCh, PushMsgAndMaxSeqCh chan common.Cmd2Value,
	loginUserID string, longConnMgr *LongConnMgr, db db_interface.DataBase, syncTimes int) (*MsgSyncer, error) {
	m := &MsgSyncer{
		loginUserID:        loginUserID,
		longConnMgr:        longConnMgr,
		PushMsgAndMaxSeqCh: PushMsgAndMaxSeqCh,
		conversationCh:     conversationCh,
		ctx:                ctx,
		syncedMaxSeqs:      make(map[string]int64),
		db:                 db,
		syncTimes:          syncTimes,
	}
	err := m.loadSeq(ctx)
	go m.DoListener()
	return m, err
}

// seq The db reads the data to the memory,set syncedMaxSeqs
func (m *MsgSyncer) loadSeq(ctx context.Context) error {
	conversations, err := m.db.GetAllConversations(ctx)
	if err != nil {
		log.ZError(ctx, "get conversation id list failed", err)
		return err
	}
	for _, conversation := range conversations {
		maxSyncedSeq, err := m.db.GetConversationNormalMsgSeq(ctx, conversation.ConversationID)
		if err != nil {
			log.ZError(ctx, "get group normal seq failed", err, "conversationID", conversation.ConversationID)
			return err
		}
		m.syncedMaxSeqs[conversation.ConversationID] = maxSyncedSeq
	}
	notificationSeqs, err := m.db.GetNotificationAllSeqs(ctx)
	if err != nil {
		log.ZError(ctx, "get notification seq failed", err)
		return err
	}
	for _, notificationSeq := range notificationSeqs {
		m.syncedMaxSeqs[notificationSeq.ConversationID] = notificationSeq.Seq
	}
	return nil
}

// DoListener Listen to the message pipe of the message synchronizer
// and process received and pushed messages
func (m *MsgSyncer) DoListener() {
	for {
		select {
		case cmd := <-m.PushMsgAndMaxSeqCh:
			m.handlePushMsgAndEvent(cmd)
		case <-m.ctx.Done():
			log.ZInfo(m.ctx, "msg syncer done, sdk logout.....")
			return
		}
	}
}

// get seqs need sync interval
func (m *MsgSyncer) getSeqsNeedSync(syncedMaxSeq, maxSeq int64) []int64 {
	var seqs []int64
	for i := syncedMaxSeq + 1; i <= maxSeq; i++ {
		seqs = append(seqs, i)
	}
	return seqs
}

// recv msg from
func (m *MsgSyncer) handlePushMsgAndEvent(cmd common.Cmd2Value) {
	switch cmd.Cmd {
	case constant.CmdConnSuccesss:
		log.ZInfo(cmd.Ctx, "recv long conn mgr connected", "cmd", cmd)
		m.doConnected(cmd.Ctx)
	case constant.CmdMaxSeq:
		log.ZInfo(cmd.Ctx, "recv max seqs from long conn mgr, start sync msgs", "cmd", cmd)
		m.compareSeqsAndBatchSync(cmd.Ctx, cmd.Value.(*sdk_struct.CmdMaxSeqToMsgSync).ConversationMaxSeqOnSvr, defaultPullNums)
	case constant.CmdPushMsg:
		m.doPushMsg(cmd.Ctx, cmd.Value.(*sdkws.PushMessages))
	}
}

func (m *MsgSyncer) compareSeqsAndBatchSync(ctx context.Context, maxSeqToSync map[string]int64, pullNums int64) {
	needSyncSeqMap := make(map[string][2]int64)
	for conversationID, maxSeq := range maxSeqToSync {
		if syncedMaxSeq, ok := m.syncedMaxSeqs[conversationID]; ok {
			if maxSeq > syncedMaxSeq {
				needSyncSeqMap[conversationID] = [2]int64{syncedMaxSeq, maxSeq}
			}
		} else {
			needSyncSeqMap[conversationID] = [2]int64{0, maxSeq}
		}
	}
	_ = m.syncAndTriggerMsgs(m.ctx, needSyncSeqMap, pullNums)
}

func (m *MsgSyncer) compareSeqsAndSync(maxSeqToSync map[string]int64) {
	for conversationID, maxSeq := range maxSeqToSync {
		if syncedMaxSeq, ok := m.syncedMaxSeqs[conversationID]; ok {
			if maxSeq > syncedMaxSeq {
				_ = m.syncAndTriggerMsgs(m.ctx, map[string][2]int64{conversationID: {syncedMaxSeq, maxSeq}}, defaultPullNums)
			}
		} else {
			_ = m.syncAndTriggerMsgs(m.ctx, map[string][2]int64{conversationID: {syncedMaxSeq, maxSeq}}, defaultPullNums)
		}
	}
}

func (m *MsgSyncer) doPushMsg(ctx context.Context, push *sdkws.PushMessages) {
	log.ZDebug(ctx, "push msgs", "push", push)
	m.pushTriggerAndSync(ctx, push.Msgs, m.triggerConversation)
	m.pushTriggerAndSync(ctx, push.NotificationMsgs, m.triggerNotification)
}

func (m *MsgSyncer) pushTriggerAndSync(ctx context.Context, pullMsgs map[string]*sdkws.PullMsgs, triggerFunc func(ctx context.Context, msgs map[string]*sdkws.PullMsgs) error) {
	if len(pullMsgs) == 0 {
		return
	}
	needSyncSeqMap := make(map[string][2]int64)
	var lastSeq int64
	var storageMsgs []*sdkws.MsgData
	for conversationID, msgs := range pullMsgs {
		for _, msg := range msgs.Msgs {
			if msg.Seq == 0 {
				_ = triggerFunc(ctx, map[string]*sdkws.PullMsgs{conversationID: {Msgs: []*sdkws.MsgData{msg}}})
				continue
			}
			lastSeq = msg.Seq
			storageMsgs = append(storageMsgs, msg)
		}
		if lastSeq == m.syncedMaxSeqs[conversationID]+int64(len(storageMsgs)) && lastSeq != 0 {
			_ = triggerFunc(ctx, map[string]*sdkws.PullMsgs{conversationID: {Msgs: storageMsgs}})
			m.syncedMaxSeqs[conversationID] = lastSeq
		} else if lastSeq != 0 { //为0就是全是通知
			needSyncSeqMap[conversationID] = [2]int64{m.syncedMaxSeqs[conversationID], lastSeq}
		}
	}
	m.syncAndTriggerMsgs(ctx, needSyncSeqMap, defaultPullNums)
}

// Called after successful reconnection to synchronize the latest message
func (m *MsgSyncer) doConnected(ctx context.Context) {
	common.TriggerCmdNotification(m.ctx, sdk_struct.CmdNewMsgComeToConversation{SyncFlag: constant.MsgSyncBegin}, m.conversationCh)
	var resp sdkws.GetMaxSeqResp
	if err := m.longConnMgr.SendReqWaitResp(m.ctx, &sdkws.GetMaxSeqReq{UserID: m.loginUserID}, constant.GetNewestSeq, &resp); err != nil {
		log.ZError(m.ctx, "get max seq error", err)
		return
	} else {
		log.ZDebug(m.ctx, "get max seq success", "resp", resp)
	}
	m.compareSeqsAndBatchSync(ctx, resp.MaxSeqs, connectPullNums)
	common.TriggerCmdNotification(m.ctx, sdk_struct.CmdNewMsgComeToConversation{SyncFlag: constant.MsgSyncEnd}, m.conversationCh)
}

// Fragment synchronization message, seq refresh after successful trigger
func (m *MsgSyncer) syncAndTriggerMsgs(ctx context.Context, seqMap map[string][2]int64, syncMsgNum int64) error {
	if len(seqMap) > 0 {
		resp, err := m.pullMsgBySeqRange(ctx, seqMap, syncMsgNum)
		if err != nil {
			log.ZError(ctx, "syncMsgFromSvr err", err, "seqMap", seqMap)
			return err
		}
		_ = m.triggerConversation(ctx, resp.Msgs)
		_ = m.triggerNotification(ctx, resp.NotificationMsgs)
		for conversationID, seqs := range seqMap {
			m.syncedMaxSeqs[conversationID] = seqs[1]
		}
		return err
	}
	return nil
}

func (m *MsgSyncer) splitSeqs(split int, seqsNeedSync []int64) (splitSeqs [][]int64) {
	if len(seqsNeedSync) <= split {
		splitSeqs = append(splitSeqs, seqsNeedSync)
		return
	}
	for i := 0; i < len(seqsNeedSync); i += split {
		end := i + split
		if end > len(seqsNeedSync) {
			end = len(seqsNeedSync)
		}
		splitSeqs = append(splitSeqs, seqsNeedSync[i:end])
	}
	return
}

func (m *MsgSyncer) pullMsgBySeqRange(ctx context.Context, seqMap map[string][2]int64, syncMsgNum int64) (resp *sdkws.PullMessageBySeqsResp, err error) {
	log.ZDebug(ctx, "pullMsgBySeqRange", "seqMap", seqMap, "syncMsgNum", syncMsgNum)
	var req sdkws.PullMessageBySeqsReq
	for conversationID, seqs := range seqMap {
		var pullNums int64 = syncMsgNum
		if pullNums > seqs[1]-seqs[0] {
			pullNums = seqs[1] - seqs[0]
		}
		req.SeqRanges = append(req.SeqRanges, &sdkws.SeqRange{
			ConversationID: conversationID,
			Begin:          seqs[0],
			End:            seqs[1],
			Num:            pullNums,
		})
	}
	resp = &sdkws.PullMessageBySeqsResp{}
	if err := m.longConnMgr.SendReqWaitResp(ctx, &req, constant.PullMsgBySeqList, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// synchronizes messages by SEQs.
func (m *MsgSyncer) syncMsgBySeqs(ctx context.Context, conversationID string, seqsNeedSync []int64) (allMsgs []*sdkws.MsgData, err error) {
	pullMsgReq := sdkws.PullMessageBySeqsReq{}
	pullMsgReq.UserID = m.loginUserID
	split := constant.SplitPullMsgNum
	seqsList := m.splitSeqs(split, seqsNeedSync)
	for i := 0; i < len(seqsList); {
		var pullMsgResp sdkws.PullMessageBySeqsResp
		err := m.longConnMgr.SendReqWaitResp(ctx, &pullMsgReq, constant.PullMsgBySeqList, &pullMsgResp)
		if err != nil {
			log.ZError(ctx, "syncMsgFromSvrSplit err", err, "pullMsgReq", pullMsgReq)
			continue
		}
		i++
		allMsgs = append(allMsgs, pullMsgResp.Msgs[conversationID].Msgs...)
	}
	return allMsgs, nil
}

// triggers a conversation with a new message.
func (m *MsgSyncer) triggerConversation(ctx context.Context, msgs map[string]*sdkws.PullMsgs) error {
	err := common.TriggerCmdNewMsgCome(ctx, sdk_struct.CmdNewMsgComeToConversation{Msgs: msgs}, m.conversationCh)
	if err != nil {
		log.ZError(ctx, "triggerCmdNewMsgCome err", err, "msgs", msgs)
	}
	return err
}

func (m *MsgSyncer) triggerNotification(ctx context.Context, msgs map[string]*sdkws.PullMsgs) error {
	err := common.TriggerCmdNotification(ctx, sdk_struct.CmdNewMsgComeToConversation{Msgs: msgs}, m.conversationCh)
	if err != nil {
		log.ZError(ctx, "triggerCmdNewMsgCome err", err, "msgs", msgs)
	}
	return err
}
