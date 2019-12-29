package main

import (
	"fmt"
	"github.com/creasty/defaults"
	"github.com/mikepb/go-serial"
	log "github.com/sirupsen/logrus"
	"github.com/vipally/binary"
)

const (
	START_MARKER    byte = 0xCC
	END_MARKER      byte = 0xCD
	T_REG_READ      byte = 0xF8
	T_REG_READ_RESP byte = 0xF6
	T_REG_WRITE     byte = 0xF9
)

const (
	REG_MODE_SELECTION         byte = 0x02
	REG_MAIN_CONTROL           byte = 0x03
	REG_STREAMING_CONTROL      byte = 0x05
	REG_STATUS                 byte = 0x06
	REG_BAUDRATE               byte = 0x07
	REG_POWER_MODE             byte = 0x0A
	REG_PRODUCT_IDENTIFICATION byte = 0x10
	REG_PRODUCT_VERSION        byte = 0x11
	REG_MAX_BAUDRATE           byte = 0x12
	REG_OUTPUT_BUFFER_LENGTH   byte = 0xE9
)

func findPort() *string {
	ports, err := serial.ListPorts()

	if err != nil {
		log.Panic(err)
	}

	for _, info := range ports {
		if info.Description() == "XB122" {
			log.Info("Found XB122 at " + info.Name())
			name := info.Name()
			return &name
		}
	}
	return nil
}

func main() {
	log.SetLevel(log.DebugLevel)

	portName := findPort()
	if portName != nil {
		options := serial.RawOptions
		options.BitRate = 115200
		options.Mode = serial.MODE_READ_WRITE

		p, err := options.Open(*portName)
		if err != nil {
			log.Panic(err)
		} else {
			log.Debug("opened port")
		}

		regValue := readRegister(p, REG_STATUS)
		log.Info(fmt.Sprintf("Status 0x%x", regValue))
		if 0 != regValue&0x00100000 {
			log.Info("STATUS: Error activating the requested service or detector")
		}
		if 0 != regValue&0x00080000 {
			log.Info("STATUS: Error creating the requested service or detector.")
		}
		if 0 != regValue&0x00040000 {
			log.Info("STATUS: Invalid Mode.")
		}
		if 0 != regValue&0x00020000 {
			log.Info("STATUS: Invalid command or parameter received..")
		}
		if 0 != regValue&0x00010000 {
			log.Info("STATUS: An error occurred in the module.")
		}
		if 0 != regValue&0x00000100 {
			log.Info("STATUS: Data is ready to be read from the buffer")
		}
		if 0 != regValue&0x00000002 {
			log.Info("STATUS: Service or detector is activated.")
		}
		if 0 != regValue&0x00000001 {
			log.Info("STATUS: Service or detector is created.")
		}

		maxBaud := readRegister(p, REG_MAX_BAUDRATE)
		log.Info("MaxBaud ", maxBaud)

		writeRegister(p, REG_POWER_MODE, 0)

		defer p.Close()
	}
}

type readRegRequest struct {
	StartMarker   byte   `default:"204"`
	PayloadLength uint16 `default:"0001"`
	RequestType   byte   `default:"248"`
	Register      byte
	EndMarker     byte `default:"205"`
}

type writeRegRequest struct {
	StartMarker   byte   `default:"204"`
	PayloadLength uint16 `default:"0005"`
	RequestType   byte   `default:"249"`
	Register      byte
	Value         uint32
	EndMarker     byte `default:"205"`
}

type writeRegResponse struct {
	StartMarker   byte
	PayloadLength uint16
	RequestType   byte
	Register      byte
	Value         uint32
	EndMarker     byte
}

type readRegResponse struct {
	StartMarker   byte
	PayloadLength uint16
	RequestType   byte
	Register      byte
	Value         uint32
	EndMarker     byte
}

func writeRegister(p *serial.Port, reg byte, val uint32) uint32 {
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
		log.Trace(bbuf)
	} else {
		log.Panic(err)
	}

	log.Debug("Send writeRegisterRequest for reg ", reg)

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

func readRegister(p *serial.Port, reg byte) uint32 {
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

	log.Debug("Send readRegisterRequest for reg ", reg)

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
