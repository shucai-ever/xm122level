package main

import (
	"fmt"
	"github.com/creasty/defaults"
	"github.com/eclipse/paho.mqtt.golang"
	"github.com/mikepb/go-serial"
	log "github.com/sirupsen/logrus"
	"github.com/vipally/binary"
	"gopkg.in/alecthomas/kingpin.v2"
	"math"
	"os"
	"os/signal"
	"syscall"
)

var p *serial.Port

const (
	START_MARKER    byte = 0xCC
	END_MARKER      byte = 0xCD
	T_REG_READ      byte = 0xF8
	T_REG_READ_RESP byte = 0xF6
	T_REG_WRITE     byte = 0xF9
)

type RegT byte

const (
	REG_MODE_SELECTION         RegT = 0x02
	REG_MAIN_CONTROL           RegT = 0x03
	REG_STREAMING_CONTROL      RegT = 0x05
	REG_STATUS                 RegT = 0x06
	REG_BAUDRATE               RegT = 0x07
	REG_POWER_MODE             RegT = 0x0A
	REG_PRODUCT_IDENTIFICATION RegT = 0x10
	REG_PRODUCT_VERSION        RegT = 0x11
	REG_MAX_BAUDRATE           RegT = 0x12
	REG_OUTPUT_BUFFER_LENGTH   RegT = 0xE9
)

const (
	MODE_POWER_BINS          uint32 = 0x0001
	MODE_ENVELOPE            uint32 = 0x0002
	MODE_IQ                  uint32 = 0x0003
	MODE_SPARSE              uint32 = 0x0004
	MODE_DISTANCE_FIXED_PEAK uint32 = 0x0100
	MODE_OBSTACLE_DETECTION  uint32 = 0x0300
	MODE_PRESENCE_DETECTOR   uint32 = 0x0400
)

const (
	MAIN_STOP              uint32 = 0
	MAIN_CREATE_FLAG_ERR   uint32 = 0x0001
	MAIN_ACTIVATE_FLAG_ERR uint32 = 0x0002
	MAIN_CREATE_ACTIVATE   uint32 = 0x0003
	MAIN_CLEAR             uint32 = 0x0004
)

//Distance Peak Fixed RegisterS
const (
	DIST_RANGE_START            RegT = 0x20
	DIST_RANGE_LENGTH           RegT = 0x21
	DIST_REPETITION_MODE        RegT = 0x22
	DIST_UPDATE_RATE            RegT = 0x23
	DIST_GAIN                   RegT = 0x24
	DIST_SENSOR_POWER_MODE      RegT = 0x25
	DIST_TX_DISABLE             RegT = 0x26
	DIST_DECREASE_TX_EMISSION   RegT = 0x27
	DIST_PROFILE_SELECTION      RegT = 0x28
	DIST_DOWNSAMPLING_FACTOR    RegT = 0x29
	DIST_HW_ACC_AVERAGE_SAMPLES RegT = 0x30
	DIST_RUN_FACTOR             RegT = 0x40
	DIST_THR_AMPLITUDE          RegT = 0x42
	DIST_SORT_BY                RegT = 0x44
	DIST_START                  RegT = 0x81
	DIST_LENGTH                 RegT = 0x82
	DIST_DATA_SATURATED         RegT = 0xA0
	DIST_MISSED_DATA            RegT = 0xA1
	DistA2                      RegT = 0xA2
	DistA3                      RegT = 0xA3
	DistReflCount               RegT = 0xB0
	DistRefl1Distance           RegT = 0xB1
	DistRefl1Amplitude          RegT = 0xB2
	DistRefl2Distance           RegT = 0xB3
	DistRefl2Amplitude          RegT = 0xB4
	DistRefl3Distance           RegT = 0xB5
	DistRefl3Amplitude          RegT = 0xB6
	DistRefl4Distance           RegT = 0xB7
	DistRefl5Amplitude          RegT = 0xB8
)

var app = kingpin.New("xm122level", "Reads distance from Acconeer XM122 and publishes to MQTT (Home Assistant)")
var serialPort = app.Flag("port", "serial port").Short('p').Required().ExistingFile()
var mqttHost = app.Flag("broker", "address of MQTT broker to connect to, e.g. tcp://mqtt.eclipse.org:1883. Env: BROKER").Short('b').Envar("BROKER").String()
var mqttUsername = app.Flag("mqttUser", "username for mqtt broker Env: BROKER_USER").Envar("BROKER_USER").String()
var mqttPassword = app.Flag("mqttPassword", "password for mqtt broker user. Env: BROKER_PW").Envar("BROKER_PW").String()
var haDiscoveryTopic = app.Flag("haDiscoTopic", "Home Assistant MQTT discovery topic, defaults to 'homeassistant'").Envar("HA_DISCO_TOPIC").Default("homeassistant").String()
var mqttRootTopic = app.Flag("rooTopic", "root topic, defaults to /xm122").Envar("WOLF_MQTT_ROOT_TOPIC").Default("xm122").String()
var sensorName = app.Flag("sensorName", "Sensor Name (for Home Assistant)").String()

func main() {
	log.SetLevel(log.DebugLevel)

	kingpin.UsageTemplate(kingpin.CompactUsageTemplate).Version("1.0").Author("vax@kgbvax.net")
	kingpin.CommandLine.Help = "XM122Level see github.com/kgbvax/xm122level for documentation."
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.MustParse(app.Parse(os.Args[1:]))

	options := serial.RawOptions
	options.BitRate = 115200
	options.Mode = serial.MODE_READ_WRITE
	var err error
	p, err = options.Open(*serialPort)
	if err != nil {
		log.Panic(err)
	} else {
		log.Debug("opened port")
	}

	hangUpChan := make(chan os.Signal)
	signal.Notify(hangUpChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-hangUpChan
		hangup()
		os.Exit(1)
	}()

	defer hangup()

	//connect to MQTT and issue HA discovery
	mqttConn := connectMQTT(*mqttHost, *mqttUsername, *mqttPassword)
	sanName := sanitizeParamName(*sensorName)

	stateTopic := registerHaDiscovery(mqttConn, sanName, *haDiscoveryTopic, *mqttRootTopic).StateTopic

	checkStatus(p, true)

	maxBaud := readRegister(p, REG_MAX_BAUDRATE)
	log.Info("MaxBaud ", maxBaud)

	writeRegister(p, REG_MODE_SELECTION, MODE_DISTANCE_FIXED_PEAK)
	checkStatus(p, true)
	log.Info("mode: ", readRegister(p, REG_MODE_SELECTION))

	log.Debug("old run factor ", readRegister(p, DIST_RUN_FACTOR))
	writeRegister(p, DIST_RUN_FACTOR, 950) //default 0,7
	checkStatus(p, true)

	log.Debug("old range start ", readRegister(p, DIST_RANGE_START))
	writeRegister(p, DIST_RANGE_START, 150)
	checkStatus(p, true)

	log.Debug("old range length ", readRegister(p, DIST_RANGE_LENGTH))
	writeRegister(p, DIST_RANGE_LENGTH, 1000) //mm
	checkStatus(p, true)

	log.Debug("old sensor power mode ", readRegister(p, DIST_SENSOR_POWER_MODE))
	writeRegister(p, DIST_SENSOR_POWER_MODE, 3)
	checkStatus(p, true)

	log.Debug("old thr amplitude ", readRegister(p, DIST_THR_AMPLITUDE))
	writeRegister(p, DIST_THR_AMPLITUDE, 150)
	checkStatus(p, true)

	log.Debug("old update rate ", readRegister(p, DIST_UPDATE_RATE))
	writeRegister(p, DIST_UPDATE_RATE, 2000) //mHz
	checkStatus(p, true)

	log.Debug("old gain  ", readRegister(p, DIST_GAIN))
	writeRegister(p, DIST_GAIN, 600)
	checkStatus(p, true)

	log.Debug("old profile ", readRegister(p, DIST_PROFILE_SELECTION))
	writeRegister(p, DIST_PROFILE_SELECTION, 1)
	checkStatus(p, true)

	log.Debug("enable streaming:_")
	writeRegister(p, REG_STREAMING_CONTROL, 1)

	log.Debug("create&activate")
	writeRegister(p, REG_MAIN_CONTROL, MAIN_CREATE_ACTIVATE)
	//checkStatus(p, true)

	for {
		publishDistanceStream(p, mqttConn, stateTopic)
	}

}

func hangup() {
	log.Info("Hangup...")
	if p != nil {
		writeRegister(p, REG_MAIN_CONTROL, 0) //stop services
		p.Close()
	}
	p = nil
}

type readRegRequest struct {
	StartMarker   byte   `default:"204"`
	PayloadLength uint16 `default:"0001"`
	RequestType   byte   `default:"248"`
	Register      RegT
	EndMarker     byte `default:"205"`
}

type writeRegRequest struct {
	StartMarker   byte   `default:"204"`
	PayloadLength uint16 `default:"0005"`
	RequestType   byte   `default:"249"`
	Register      RegT
	Value         uint32
	EndMarker     byte `default:"205"`
}

type writeRegResponse struct {
	StartMarker   byte
	PayloadLength uint16
	RequestType   byte
	Register      RegT
	Value         uint32
	EndMarker     byte
}

type readRegResponse struct {
	StartMarker   byte
	PayloadLength uint16
	RequestType   byte
	Register      RegT
	Value         uint32
	EndMarker     byte
}

func writeRegister(p *serial.Port, reg RegT, val uint32) uint32 {
	req := &writeRegRequest{Value: val, Register: reg}
	err := defaults.Set(req)
	if err != nil {
		log.Fatal(err)
	}

	if bbuf, err := binary.Encode(req, nil); err == nil {
		_, err := p.Write(bbuf)
		if err != nil {
			log.Panic(err)
		}
		log.Debug(bbuf)
	} else {
		log.Panic(err)
	}
	log.Debugf("set reg %#x to %#x (%v)", reg, val, val)

	resp := &writeRegResponse{}
	sz := binary.Size(resp)
	buffer := make([]byte, sz)
	numRead, err := p.Read(buffer)
	if numRead != sz {
		log.Warn("did not recieve expected data, got vs expected: ", numRead, " ", sz)
	}
	if err != nil {
		log.Error(err)
	}
	err = binary.Decode(buffer, resp)
	if err != nil {
		log.Error(err)
	}
	return resp.Value

}

func publishDistanceStream(p *serial.Port, cl mqtt.Client, topic string) {
	var buf1 [1]byte
	var buf2 [2]byte

	_, _ = p.Read(buf1[:]) //start

	_, _ = p.Read(buf2[:]) //len
	len := binary.LittleEndian.Uint16(buf2[:])
	//log.Debugf("Stream msg len: %v",len)

	_, _ = p.Read(buf1[:]) // type
	var msgBuf []byte = make([]byte, len)

	p.Read(msgBuf)

	entries := decodeStreamingPayloadDistance(msgBuf)

	//pick value with max(ax)
	var maxAx uint16 = 0
	var maxAxVal float32
	for _, entry := range entries {
		if maxAx < entry.ax {
			maxAx = entry.ax
			maxAxVal = entry.dist * 1000.0
		}
	}
	log.Debugf("#VAL %f", maxAxVal)

	pub(cl, topic, fmt.Sprintf("%f", maxAxVal))

	_, _ = p.Read(buf1[:]) //end
}

type distEntry struct {
	dist float32 //mm
	ax   uint16  //assumed to be amplitude, no documentation available
}

func decodeStreamingPayloadDistance(buf []byte) []distEntry {
	offset := 1
	var entries []distEntry = nil

	resultInfoLength := int(binary.LittleEndian.Uint16(buf[offset : offset+2]))
	offset += 2

	//log.Debugf("ResultInfo: %x",buf[offset:offset+resultInfoLength])
	offset += resultInfoLength
	offset += 1 //skip over buffer marker
	bufferLength := int(binary.LittleEndian.Uint16(buf[offset : offset+2]))
	//log.Debugf("BufferLen: %v",bufferLength)
	offset += 2
	if bufferLength > 0 {
		numItems := bufferLength / 6
		entries = make([]distEntry, numItems)
		//log.Debugf("items(%v)",numItems)
		for i := 0; i < numItems; i++ {
			distbits := binary.LittleEndian.Uint32(buf[offset : offset+4])
			f1 := math.Float32frombits(distbits)
			offset += 4
			ax := binary.LittleEndian.Uint16(buf[offset : offset+2])
			offset += 2
			log.Debugf("f1: %v ax: %v", f1*100.0, ax)
			entries[i].dist = f1
			entries[i].ax = ax
		}
	}
	return entries
}

func readRegister(p *serial.Port, reg RegT) uint32 {
	req := &readRegRequest{Register: reg}
	err := defaults.Set(req)
	if err != nil {
		log.Fatal(err)
	}

	if bbuf, err := binary.Encode(req, nil); err == nil {
		_, err := p.Write(bbuf)
		if err != nil {
			log.Panic(err)
		}
		log.Trace(bbuf)
	} else {
		log.Panic(err)
	}

	resp := &readRegResponse{}
	sz := binary.Size(resp)
	buffer := make([]byte, sz)
	numRead, err := p.Read(buffer)
	if numRead != sz {
		log.Warn("did not recieve expected data, got vs expected: ", numRead, " ", sz)
	}
	if err != nil {
		log.Error(err)
	}
	err = binary.Decode(buffer, resp)
	if err != nil {
		log.Error(err)
	}
	return resp.Value
}

const (
	StatusErrActivating  = 0x00100000
	StatusErrCreating    = 0x00080000
	StatusInvalidMode    = 0x00040000
	StatusInvalidCommand = 0x00020000
	StatusDataReady      = 0x00000100
	StatusError          = 0x00010000
	StatusServActivated  = 0x00000002
	StatusServ           = 0x00000001
)

func checkStatus(p *serial.Port, printStatus bool) uint32 {
	regValue := readRegister(p, REG_STATUS)
	log.Trace(fmt.Sprintf("Status 0x%x", regValue))
	if printStatus {
		if 0 != regValue&StatusErrActivating {
			log.Info("STATUS: Error activating the requested service or detector")
		}
		if 0 != regValue&StatusErrCreating {
			log.Info("STATUS: Error creating the requested service or detector.")
		}
		if 0 != regValue&StatusInvalidMode {
			log.Info("STATUS: Invalid Mode.")
		}
		if 0 != regValue&StatusInvalidCommand {
			log.Info("STATUS: Invalid command or parameter received..")
		}
		if 0 != regValue&StatusError {
			log.Info("STATUS: An error occurred in the module.")
		}
		if 0 != regValue&StatusDataReady {
			log.Info("STATUS: Data is ready to be read from the buffer")
		}
		if 0 != regValue&StatusServActivated {
			log.Info("STATUS: Service or detector is activated.")
		}
		if 0 != regValue&StatusServ {
			log.Info("STATUS: Service or detector is created.")
		}
	}
	return regValue
}
