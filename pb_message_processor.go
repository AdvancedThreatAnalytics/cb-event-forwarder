package main

import (
	"errors"
	"fmt"
	"github.com/carbonblack/cb-event-forwarder/sensor_events"
	"github.com/golang/protobuf/proto"
	"strings"
)

func GetProcessGUID(m *sensor_events.CbEventMsg) string {
	if m.Header.ProcessPid != nil && m.Header.ProcessCreateTime != nil && m.Env != nil &&
		m.Env.Endpoint != nil && m.Env.Endpoint.SensorId != nil {
		pid := m.Header.GetProcessPid()
		create_time := m.Header.GetProcessCreateTime()
		sensor_id := m.Env.Endpoint.GetSensorId()

		return MakeGUID(sensor_id, pid, create_time)
	} else {
		return fmt.Sprintf("%d", m.Header.GetProcessGuid())
	}
}

type ConvertedCbMessage struct {
	OriginalMessage *sensor_events.CbEventMsg
}

func (inmsg *ConvertedCbMessage) getStringByGuid(guid int64) (string, error) {
	for _, rawString := range inmsg.OriginalMessage.GetStrings() {
		if rawString.GetGuid() == guid {
			return GetUnicodeFromUTF8(rawString.GetUtf8String()), nil
		}
	}
	return "", errors.New(fmt.Sprintf("Could not find string for id %d", guid))
}

func ProcessProtobufMessage(routingKey string, body []byte) (map[string]interface{}, error) {
	cbMessage := new(sensor_events.CbEventMsg)
	err := proto.Unmarshal(body, cbMessage)
	if err != nil {
		return nil, err
	}

	inmsg := &ConvertedCbMessage{
		OriginalMessage: cbMessage,
	}

	outmsg := make(map[string]interface{})
	outmsg["timestamp"] = WindowsTimeToUnixTime(inmsg.OriginalMessage.Header.GetTimestamp())
	outmsg["type"] = routingKey

	outmsg["sensor_id"] = cbMessage.Env.Endpoint.GetSensorId()
	outmsg["computer_name"] = cbMessage.Env.Endpoint.GetSensorHostName()

	// is the message from an endpoint event process?
	eventMsg := true

	switch {
	case cbMessage.Process != nil:
		WriteProcessMessage(inmsg, outmsg)
	case cbMessage.Modload != nil:
		WriteModloadMessage(inmsg, outmsg)
	case cbMessage.Filemod != nil:
		WriteFilemodMessage(inmsg, outmsg)
	case cbMessage.Network != nil:
		WriteNetconnMessage(inmsg, outmsg)
	case cbMessage.Regmod != nil:
		WriteRegmodMessage(inmsg, outmsg)
	case cbMessage.Childproc != nil:
		WriteChildprocMessage(inmsg, outmsg)
	case cbMessage.Crossproc != nil:
		WriteCrossProcMessge(inmsg, outmsg)
	case cbMessage.Emet != nil:
		WriteEmetEvent(inmsg, outmsg)
	case cbMessage.NetconnBlocked != nil:
		WriteNetconnBlockedMessage(inmsg, outmsg)
	case cbMessage.TamperAlert != nil:
		eventMsg = false
		WriteTamperAlertMsg(inmsg, outmsg)
	case cbMessage.Blocked != nil:
		eventMsg = false
		WriteProcessBlockedMsg(inmsg, outmsg)
	case cbMessage.Module != nil:
		eventMsg = false
		WriteModinfoMessage(inmsg, outmsg)
	default:
		return nil, errors.New("Unknown event type encountered")
	}

	// write metadata about the process in case this message is generated by a process on an endpoint
	if eventMsg {
		outmsg["process_guid"] = GetProcessGUID(cbMessage)
		outmsg["pid"] = inmsg.OriginalMessage.Header.GetProcessPid()
		if _, ok := outmsg["md5"]; !ok {
			outmsg["md5"] = GetMd5Hexdigest(inmsg.OriginalMessage.Header.GetProcessMd5())
		}
	}

	return outmsg, nil
}

func WriteProcessMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "proc"

	file_path, _ := message.getStringByGuid(message.OriginalMessage.Header.GetFilepathStringGuid())
	kv["path"] = file_path

	// hack to rewrite the "type" since the Cb server may make incoming process events "ingress.event.process" or
	// "ingress.event.procstart"

	if message.OriginalMessage.Process.GetCreated() {
		kv["type"] = "ingress.event.procstart"
		if message.OriginalMessage.Process.Md5Hash != nil {
			kv["md5"] = GetMd5Hexdigest(message.OriginalMessage.Process.GetMd5Hash())
		}
	} else {
		kv["type"] = "ingress.event.procend"
	}

	kv["command_line"] = GetUnicodeFromUTF8(message.OriginalMessage.Process.GetCommandline())

	om := message.OriginalMessage
	kv["parent_process_guid"] = MakeGUID(om.Env.Endpoint.GetSensorId(), om.Process.GetParentPid(),
		om.Process.GetParentCreateTime())

	if message.OriginalMessage.Process.Username != nil {
		kv["username"] = message.OriginalMessage.Process.GetUsername()
	}
}

func WriteModloadMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "modload"

	file_path, _ := message.getStringByGuid(message.OriginalMessage.Header.GetFilepathStringGuid())
	kv["path"] = file_path
	kv["md5"] = GetMd5Hexdigest(message.OriginalMessage.Modload.GetMd5Hash())

}

func filemodAction(a sensor_events.CbFileModMsg_CbFileModAction) string {
	switch a {
	case sensor_events.CbFileModMsg_actionFileModCreate:
		return "create"
	case sensor_events.CbFileModMsg_actionFileModWrite:
		return "write"
	case sensor_events.CbFileModMsg_actionFileModDelete:
		return "delete"
	case sensor_events.CbFileModMsg_actionFileModLastWrite:
		return "lastwrite"
	}
	return fmt.Sprintf("unknown (%d)", int32(a))
}

func WriteFilemodMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "filemod"

	file_path, _ := message.getStringByGuid(message.OriginalMessage.Header.GetFilepathStringGuid())
	kv["path"] = file_path

	action := message.OriginalMessage.Filemod.GetAction()
	kv["action"] = filemodAction(action)
	kv["actiontype"] = int32(action)
}

func WriteChildprocMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "childproc"

	kv["created"] = message.OriginalMessage.Childproc.GetCreated()

	om := message.OriginalMessage
	if om.Childproc.Pid != nil && om.Childproc.CreateTime != nil && om.Env != nil &&
		om.Env.Endpoint != nil && om.Env.Endpoint.SensorId != nil {
		pid := om.Childproc.GetPid()
		create_time := om.Childproc.GetCreateTime()
		sensor_id := om.Env.Endpoint.GetSensorId()

		// for some reason, the Childproc.pid field is an int64 and not an int32 as it is in the process header
		// convert the pid to int32
		pid32 := int32(pid & 0xffffffff)

		kv["child_process_guid"] = MakeGUID(sensor_id, pid32, create_time)
	} else {
		kv["child_process_guid"] = om.Childproc.GetChildGuid()
	}

	kv["md5"] = GetMd5Hexdigest(message.OriginalMessage.Childproc.GetMd5Hash())
}

func regmodAction(a sensor_events.CbRegModMsg_CbRegModAction) string {
	switch a {
	case sensor_events.CbRegModMsg_actionRegModCreateKey:
		return "createkey"
	case sensor_events.CbRegModMsg_actionRegModWriteValue:
		return "writeval"
	case sensor_events.CbRegModMsg_actionRegModDeleteKey:
		return "delkey"
	case sensor_events.CbRegModMsg_actionRegModDeleteValue:
		return "delval"
	}
	return fmt.Sprintf("unknown (%d)", int32(a))
}

func WriteRegmodMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "regmod"

	kv["path"] = GetUnicodeFromUTF8(message.OriginalMessage.Regmod.GetUtf8Regpath())

	action := message.OriginalMessage.Regmod.GetAction()
	kv["action"] = regmodAction(action)
	kv["actiontype"] = int32(action)
}

func WriteNetconnMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "netconn"

	kv["domain"] = GetUnicodeFromUTF8(message.OriginalMessage.Network.GetUtf8Netpath())
	kv["ipv4"] = GetIPv4Address(message.OriginalMessage.Network.GetIpv4Address())
	kv["port"] = ntohs(uint16(message.OriginalMessage.Network.GetPort()))
	kv["protocol"] = int32(message.OriginalMessage.Network.GetProtocol())

	if message.OriginalMessage.Network.GetOutbound() {
		kv["direction"] = "outbound"
	} else {
		kv["direction"] = "inbound"
	}

	//
	// In CB 5.1 local and remote ip/port were added.  They aren't guaranteed
	// to be there (b/c we have an older sensor) or in some cases we cannot
	// determine them

	if (message.OriginalMessage.Network.RemoteIpAddress != nil) {
		kv["remote_ip"] = GetIPv4Address(message.OriginalMessage.Network.GetRemoteIpAddress())
		kv["remote_port"] = ntohs(uint16(message.OriginalMessage.Network.GetRemotePort()))
	}

	if (message.OriginalMessage.Network.LocalIpAddress != nil){
		kv["local_ip"] = GetIPv4Address(message.OriginalMessage.Network.GetLocalIpAddress())
		kv["local_port"] = ntohs(uint16(message.OriginalMessage.Network.GetRemotePort()))
	}


}

func WriteModinfoMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "binary_info"
	kv["md5"] = strings.ToUpper(string(message.OriginalMessage.Module.GetMd5()))
	kv["size"] = message.OriginalMessage.Module.GetOriginalModuleLength()

	digsigResult := make(map[string]interface{})
	digsigResult["result"] = message.OriginalMessage.Module.GetUtf8_DigSig_Result()

	kv["digsig"] = digsigResult
}

func emetMitigationType(a *sensor_events.CbEmetMitigationAction) string {

	mitigation := a.GetMitigationType()

	switch mitigation {
	case sensor_events.CbEmetMitigationAction_actionDep:
		return "Dep"
	case sensor_events.CbEmetMitigationAction_actionSehop:
		return "Sehop"
	case sensor_events.CbEmetMitigationAction_actionAsr:
		return "Asr"
	case sensor_events.CbEmetMitigationAction_actionAslr:
		return "Aslr"
	case sensor_events.CbEmetMitigationAction_actionNullPage:
		return "NullPage"
	case sensor_events.CbEmetMitigationAction_actionHeapSpray:
		return "HeapSpray"
	case sensor_events.CbEmetMitigationAction_actionMandatoryAslr:
		return "MandatoryAslr"
	case sensor_events.CbEmetMitigationAction_actionEaf:
		return "Eaf"
	case sensor_events.CbEmetMitigationAction_actionEafPlus:
		return "EafPlus"
	case sensor_events.CbEmetMitigationAction_actionBottomUpAslr:
		return "BottomUpAslr"
	case sensor_events.CbEmetMitigationAction_actionLoadLibrary:
		return "LoadLibrary"
	case sensor_events.CbEmetMitigationAction_actionMemoryProtection:
		return "MemoryProtection"
	case sensor_events.CbEmetMitigationAction_actionSimulateExecFlow:
		return "SimulateExecFlow"
	case sensor_events.CbEmetMitigationAction_actionStackPivot:
		return "StackPivot"
	case sensor_events.CbEmetMitigationAction_actionCallerChecks:
		return "CallerChecks"
	case sensor_events.CbEmetMitigationAction_actionBannedFunctions:
		return "BannedFunctions"
	case sensor_events.CbEmetMitigationAction_actionDeepHooks:
		return "DeepHooks"
	case sensor_events.CbEmetMitigationAction_actionAntiDetours:
		return "AntiDetours"

	}
	return fmt.Sprintf("unknown (%d)", int32(mitigation))
}

func WriteEmetEvent(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "emet_mitigation"
	kv["log_message"] = message.OriginalMessage.Emet.GetActionText()
	kv["mitigation"] = emetMitigationType(message.OriginalMessage.Emet.GetAction())
	kv["blocked"] = message.OriginalMessage.Emet.GetBlocked()
	kv["log_id"] = message.OriginalMessage.Emet.GetEmetId()
	kv["emet_timestamp"] = message.OriginalMessage.Emet.GetEmetTimstamp()
}

func crossprocOpenType(a sensor_events.CbCrossProcessOpenMsg_OpenType) string {
	switch a {
	case sensor_events.CbCrossProcessOpenMsg_OpenProcessHandle:
		return "open_process"
	case sensor_events.CbCrossProcessOpenMsg_OpenThreadHandle:
		return "open_thread"
	}
	return fmt.Sprintf("unknown (%d)", int32(a))
}

func WriteCrossProcMessge(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "cross_process"

	om := message.OriginalMessage

	if message.OriginalMessage.Crossproc.Open != nil{
		open := message.OriginalMessage.Crossproc.Open

		kv["cross_process_type"] = crossprocOpenType(open.GetType())

		kv["requested_acces"] = open.GetRequestedAccess()
		kv["target_pid"] = open.GetTargetPid()
		kv["target_create_time"] = open.GetTargetProcCreateTime()
		kv["target_md5"] = GetMd5Hexdigest(open.GetTargetProcMd5())
		kv["target_path"] = open.GetTargetProcPath()

		pid32 := int32(open.GetTargetPid() & 0xffffffff)
		kv["target_process_guid"] = MakeGUID(om.Env.Endpoint.GetSensorId(), pid32, int64(open.GetTargetProcCreateTime()))


	}else {
		rt := message.OriginalMessage.Crossproc.Remotethread

		kv["cross_process_type"] = "remote_thread"
		kv["target_pid"] = rt.GetRemoteProcPid()
		kv["target_create_time"] = rt.GetRemoteProcCreateTime()
		kv["target_md5"] = GetMd5Hexdigest(rt.GetRemoteProcMd5())
		kv["target_path"] = rt.GetRemoteProcPath()

		kv["target_process_guid"] = MakeGUID(om.Env.Endpoint.GetSensorId(), int32(rt.GetRemoteProcPid()), int64(rt.GetRemoteProcCreateTime()))
	}

}

func tamperAlertType(a sensor_events.CbTamperAlertMsg_CbTamperAlertType) string {
	switch a {
	case sensor_events.CbTamperAlertMsg_AlertCoreDriverUnloaded:
		return "CoreDriverUnloaded"
	case sensor_events.CbTamperAlertMsg_AlertNetworkDriverUnloaded:
		return "NetworkDriverUnloaded"
	case sensor_events.CbTamperAlertMsg_AlertCbServiceStopped:
		return "CbServiceStopped"
	case sensor_events.CbTamperAlertMsg_AlertCbProcessTerminated:
		return "CbProcessTerminated"
	case sensor_events.CbTamperAlertMsg_AlertCbCodeInjection:
		return "CbCodeInjection"
	}
	return fmt.Sprintf("unknown (%d)", int32(a))
}

func WriteTamperAlertMsg(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "tamper"
	kv["tamper_type"] = tamperAlertType((message.OriginalMessage.TamperAlert.GetType()))
}

func blockedProcessEventType(a sensor_events.CbProcessBlockedMsg_BlockEvent) string{

	switch a {
	case sensor_events.CbProcessBlockedMsg_ProcessCreate:
		return "ProcessCreate"
	case sensor_events.CbProcessBlockedMsg_RunningProcess:
		return "RunningProcess"
	}
	return fmt.Sprintf("unknown (%d)", int32(a))
}

func blockedProcessResult(a sensor_events.CbProcessBlockedMsg_BlockResult) string {

	switch a {
	case sensor_events.CbProcessBlockedMsg_ProcessTerminated:
		return "ProcessTerminated"
	case sensor_events.CbProcessBlockedMsg_NotTerminatedCBProcess:
		return "NotTerminatedCBProcess"
	case sensor_events.CbProcessBlockedMsg_NotTerminatedSystemProcess:
		return "NotTerminatedSystemProcess"
	case sensor_events.CbProcessBlockedMsg_NotTerminatedCriticalSystemProcess:
		return "NotTerminatedCriticalSystemProcess"
	case sensor_events.CbProcessBlockedMsg_NotTerminatedWhitelistedPath:
		return "NotTerminatedWhitelistPath"
	case sensor_events.CbProcessBlockedMsg_NotTerminatedOpenProcessError:
		return "NotTerminatedOpenProcessError"
	case sensor_events.CbProcessBlockedMsg_NotTerminatedTerminateError:
		return "NotTerminatedTerminateError"
	}
	return fmt.Sprintf("unknown (%d)", int32(a))
}

func WriteProcessBlockedMsg(message *ConvertedCbMessage, kv map[string]interface{}){

	block := message.OriginalMessage.Blocked
	kv["event_type"] = "blocked_process"

	if block.GetBlockedType() == sensor_events.CbProcessBlockedMsg_MD5Hash{
		kv["blocked_reason"] = "Md5Hash"
	}else {
		kv["blocked_reason"] = fmt.Sprintf("unknown (%d)", int32(block.GetBlockedType()))
	}

	kv["blocked_event"] = blockedProcessEventType(block.GetBlockedEvent())
	kv["blocked_md5"] = GetMd5Hexdigest(block.GetBlockedmd5Hash())
	kv["blocked_path"] = block.GetBlockedPath()
	kv["blocked_result"] = blockedProcessResult(block.GetBlockResult())

	if block.GetBlockResult() == sensor_events.CbProcessBlockedMsg_NotTerminatedOpenProcessError ||
	    block.GetBlockResult() == sensor_events.CbProcessBlockedMsg_NotTerminatedTerminateError {
		kv["blocked_error"] = block.GetBlockError()
	}

	if block.BlockedPid != nil{
		kv["blocked_pid"] = block.GetBlockedPid()
		kv["blocked_process_createtime"] = block.GetBlockedProcCreateTime()

		om := message.OriginalMessage
		kv["target_process_guid"] = MakeGUID(om.Env.Endpoint.GetSensorId(), int32(block.GetBlockedPid()), int64(block.GetBlockedProcCreateTime()))
	}

	kv["blocked_commandline"] = block.GetBlockedCmdline()

	if (block.GetBlockedEvent() == sensor_events.CbProcessBlockedMsg_ProcessCreate &&
	    block.GetBlockResult() == sensor_events.CbProcessBlockedMsg_ProcessTerminated) {
		kv["blocked_uid"] = block.GetBlockedUid()
		kv["blocked_username"] = block.GetBlockedUsername()
	}
}


func WriteNetconnBlockedMessage(message *ConvertedCbMessage, kv map[string]interface{}) {
	kv["event_type"] = "blocked_netconn"

	blocked := message.OriginalMessage.NetconnBlocked

	kv["domain"] = GetUnicodeFromUTF8(blocked.GetUtf8Netpath())
	kv["ipv4"] = GetIPv4Address(blocked.GetIpv4Address())
	kv["port"] = ntohs(uint16(blocked.GetPort()))
	kv["protocol"] = int32(blocked.GetProtocol())

	if blocked.GetOutbound() {
		kv["direction"] = "outbound"
	} else {
		kv["direction"] = "inbound"
	}
	if (blocked.RemoteIpAddress != nil) {
		kv["remote_ip"] = GetIPv4Address(blocked.GetRemoteIpAddress())
		kv["remote_port"] = ntohs(uint16(blocked.GetRemotePort()))
	}

	if (blocked.LocalIpAddress != nil) {
		kv["local_ip"] = GetIPv4Address(blocked.GetLocalIpAddress())
		kv["local_port"] = ntohs(uint16(blocked.GetRemotePort()))
	}
}


// TODO: WriteNetconnBlockedMessage
