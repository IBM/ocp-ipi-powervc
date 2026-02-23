// Copyright 2025 IBM Corp
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
//	"bytes"
	"encoding/json"
	"fmt"
//	"io"
	"io/ioutil"
	"net"
)

//	buffer := make([]byte, 1024)
//	n, err := conn.Read(buffer)
// or

//	_, err = io.Copy(&buf, conn)
//      buf.Len()
//	buf.String()
// or

//	reader := bufio.NewReader(conn)
//	data, err := reader.ReadString('\n')
// or

type CommandHeader struct {
        Command string `json:"Command"`
}

type CommandCheckAlive struct {
	Command string           `json:"Command"`
}

type CommandIsAlive struct {
	Command string           `json:"Command"`
	Result  string           `json:"Result"`
}

type CommandCreateBastion struct {
	Command    string        `json:"Command"`
	CloudName  string        `json:"cloudName"`
	ServerName string        `json:"serverName"`
	DomainName string        `json:"domainName"`
}

type CommandBastionCreated struct {
	Command string           `json:"Command"`
	Result  string           `json:"Result"`
}

type CommandSendMetadata struct {
	Command  string          `json:"Command"`
	Metadata CreateMetadata
}

func sendByteArray(conn net.Conn, ab []byte) (err error) {
	_, err = conn.Write(ab)
	if err != nil {
		log.Debugf("sendByteArray: conn.Write(ab) returns %v", err)
		return
	}

	_, err = conn.Write([]byte("\n"))
	if err != nil {
		log.Debugf("sendByteArray: conn.Write(\"\\n\") returns %v", err)
		return
	}

	return
}

func receiveResponse(conn net.Conn) (response string, err error) {
	reader := bufio.NewReader(conn)

	response, err = reader.ReadString('\n')

	return
}

func sendCheckAlive(serverIP string) error {
	var (
		cmdIn          CommandCheckAlive
		cmdOut         CommandIsAlive
		marshalledData []byte
		response       string
		err            error
	)

	cmdIn = CommandCheckAlive{
		Command: "check-alive",
	}

	log.Debugf("sendCheckAlive: serverIP = %s", serverIP)

	// Avoid: address format "%s:%s" does not work with IPv6
	// Connect to the server
	conn, err := net.Dial("tcp", net.JoinHostPort(serverIP, "8080"))
	if err != nil {
		log.Debugf("sendCheckAlive: net.Dial returns %v", err)
		return err
	}

	// Close the connection when we're done
	defer conn.Close()

	marshalledData, err = json.Marshal(cmdIn)
	if err != nil {
		log.Debugf("sendCheckAlive: json.Marshal returns %v", err)
		return err
	}
	log.Debugf("sendAlive: marshalledData = %v", string(marshalledData))

	// Send the command to the server
	err = sendByteArray(conn, marshalledData)
	if err != nil {
		return err
	}

	response, err = receiveResponse(conn)
	if err != nil {
		log.Debugf("sendCheckAlive: receiveResponse returns %v", err)
		// if err != io.EOF {
		return err
	}
	log.Debugf("sendCheckAlive: read: %s", response)

	err = json.Unmarshal([]byte(response), &cmdOut)
	if err != nil {
		log.Debugf("sendCheckAlive: json.Unmarshal returns %v", err)
		return err
	}
	log.Debugf("sendCheckAlive: cmdOut = %+v", cmdOut)

	if cmdOut.Result != "" {
		return fmt.Errorf("%s", cmdOut.Result)
	}

	log.Debugf("sendCheckAlive: Done!")

	return nil
}

func sendCreateBastion(serverIP string, cloudName string, serverName string, domainName string) error {
	var (
		cmdIn          CommandCreateBastion
		cmdOut         CommandBastionCreated
		marshalledData []byte
		response       string
		err            error
	)

	cmdIn = CommandCreateBastion{
		Command:    "create-bastion",
		CloudName:  cloudName,
		ServerName: serverName,
		DomainName: domainName,
	}

	log.Debugf("sendCreateBastion: serverIP = %s", serverIP)

	// Avoid: address format "%s:%s" does not work with IPv6
	// Connect to the server
	conn, err := net.Dial("tcp", net.JoinHostPort(serverIP, "8080"))
	if err != nil {
		log.Debugf("sendCreateBastion: net.Dial returns %v", err)
		return err
	}

	// Close the connection when we're done
	defer conn.Close()

	marshalledData, err = json.Marshal(cmdIn)
	if err != nil {
		log.Debugf("sendCreateBastion: json.Marshal returns %v", err)
		return err
	}
	log.Debugf("sendCreateBastion: marshalledData = %v", string(marshalledData))

	// Send the command to the server
	err = sendByteArray(conn, marshalledData)
	if err != nil {
		return err
	}

	response, err = receiveResponse(conn)
	if err != nil {
		log.Debugf("sendCreateBastion: receiveResponse returns %v", err)
		// if err != io.EOF {
		return err
	}
	log.Debugf("sendCreateBastion: read: %s", response)

	err = json.Unmarshal([]byte(response), &cmdOut)
	if err != nil {
		log.Debugf("sendCreateBastion: json.Unmarshal returns %v", err)
		return err
	}
	log.Debugf("sendCreateBastion: cmdOut = %+v", cmdOut)

	if cmdOut.Result != "" {
		return fmt.Errorf("%s", cmdOut.Result)
	}

	log.Debugf("sendCreateBastion: Done!")

	return nil
}

func sendMetadata(metadataFile string, serverIP string, shouldCreateMetadata bool) error {
	var (
		content        []byte
		cmd            CommandSendMetadata
		marshalledData []byte
		err            error
	)

	// Avoid: address format "%s:%s" does not work with IPv6
	// Connect to the server
	conn, err := net.Dial("tcp", net.JoinHostPort(serverIP, "8080"))
	if err != nil {
		log.Debugf("sendMetadata: net.Dial returns %v", err)
		return err
	}

	// Close the connection when we're done
	defer conn.Close()

	// Read metadata.json into a buffer
	content, err = ioutil.ReadFile(metadataFile)
	if err != nil {
		log.Debugf("sendMetadata: ioutil.ReadFile returns %v", err)
		return err
	}
	log.Debugf("sendMetadata: content = %s", content)

	// Create the command JSON structure
	if shouldCreateMetadata {
		cmd.Command = "create-metadata"
	} else {
		cmd.Command = "delete-metadata"
	}
	err = json.Unmarshal(content, &cmd.Metadata)
	if err != nil {
		log.Debugf("sendMetadata: json.Unmarshal return %v", err)
		return err
	}
	log.Debugf("sendMetadata: cmd = %+v", cmd)

	marshalledData, err = json.Marshal(cmd)
	if err != nil {
		log.Debugf("sendMetadata: json.Marshal returns %v", err)
		return err
	}
	log.Debugf("sendMetadata: marshalledData = %v", string(marshalledData))

	// Send the command to the server
	err = sendByteArray(conn, marshalledData)
	if err != nil {
		return err
	}

	log.Debugf("sendMetadata: Done!")

	return nil
}
