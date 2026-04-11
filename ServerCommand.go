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
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

//      buffer := make([]byte, 1024)
//      n, err := conn.Read(buffer)
// or

//      _, err = io.Copy(&buf, conn)
//      buf.Len()
//      buf.String()
// or

//      reader := bufio.NewReader(conn)
//      data, err := reader.ReadString('\n')
// or

const (
	// Server communication constants
	serverPort = "8080"

	// Command name constants
	serverCmdCheckAlive      = "check-alive"
	serverCmdCreateBastion   = "create-bastion"
	serverCmdCreateMetadata  = "create-metadata"
	serverCmdDeleteMetadata  = "delete-metadata"
)

// CommandHeader represents the base command structure with just the command name.
type CommandHeader struct {
	Command string `json:"Command"`
}

// CommandCheckAlive represents a request to check if the server is alive.
type CommandCheckAlive struct {
	Command string `json:"Command"`
}

// CommandIsAlive represents the response to a check-alive request.
type CommandIsAlive struct {
	Command string `json:"Command"`
	Result  string `json:"Result"`
}

// CommandCreateBastion represents a request to create a bastion host.
type CommandCreateBastion struct {
	Command    string `json:"Command"`
	CloudName  string `json:"cloudName"`
	ServerName string `json:"serverName"`
	DomainName string `json:"domainName"`
}

// CommandBastionCreated represents the response to a create-bastion request.
type CommandBastionCreated struct {
	Command string `json:"Command"`
	Result  string `json:"Result"`
}

// CommandSendMetadata represents a request to send metadata to the server.
type CommandSendMetadata struct {
	Command  string         `json:"Command"`
	Metadata CreateMetadata
}

// sendByteArray sends a byte array to the server connection followed by a newline.
// This is a helper function for sending JSON-encoded commands.
//
// Parameters:
//   - conn: Network connection to the server
//   - ab: Byte array to send
//
// Returns:
//   - error: Any error encountered during sending
func sendByteArray(conn net.Conn, ab []byte) (err error) {
	if conn == nil {
		return fmt.Errorf("connection cannot be nil")
	}
	if len(ab) == 0 {
		return fmt.Errorf("byte array cannot be empty")
	}

	_, err = conn.Write(ab)
	if err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	_, err = conn.Write([]byte("\n"))
	if err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	log.Debugf("sendByteArray: Successfully sent %d bytes", len(ab))
	return nil
}

// receiveResponse reads a newline-terminated response from the server connection.
//
// Parameters:
//   - conn: Network connection to the server
//
// Returns:
//   - string: Response string received from the server
//   - error: Any error encountered during reading
func receiveResponse(conn net.Conn) (response string, err error) {
	if conn == nil {
		return "", fmt.Errorf("connection cannot be nil")
	}

	reader := bufio.NewReader(conn)
	response, err = reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	log.Debugf("receiveResponse: Received %d bytes", len(response))
	return response, nil
}

// sendCheckAlive sends a check-alive command to the server to verify it's responsive.
//
// Parameters:
//   - serverIP: IP address of the server to check
//
// Returns:
//   - error: Any error encountered during the check
func sendCheckAlive(serverIP string) error {
	if serverIP == "" {
		return fmt.Errorf("server IP cannot be empty")
	}

	var (
		cmdIn          CommandCheckAlive
		cmdOut         CommandIsAlive
		marshalledData []byte
		response       string
		err            error
	)

	cmdIn = CommandCheckAlive{
		Command: serverCmdCheckAlive,
	}

	log.Debugf("sendCheckAlive: Connecting to server at %s", serverIP)

	// Use net.JoinHostPort to properly handle IPv6 addresses
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(serverIP, serverPort), 10 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to server %s:%s: %w", serverIP, serverPort, err)
	}
	defer conn.Close()

	marshalledData, err = json.Marshal(cmdIn)
	if err != nil {
		return fmt.Errorf("failed to marshal check-alive command: %w", err)
	}
	log.Debugf("sendCheckAlive: Sending command: %s", string(marshalledData))

	// Send the command to the server
	err = sendByteArray(conn, marshalledData)
	if err != nil {
		return fmt.Errorf("failed to send check-alive command: %w", err)
	}

	response, err = receiveResponse(conn)
	if err != nil {
		return fmt.Errorf("failed to receive response: %w", err)
	}
	log.Debugf("sendCheckAlive: Received response: %s", response)

	err = json.Unmarshal([]byte(response), &cmdOut)
	if err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}
	log.Debugf("sendCheckAlive: Parsed response: %+v", cmdOut)

	if cmdOut.Result != "" {
		return fmt.Errorf("server returned error: %s", cmdOut.Result)
	}

	log.Debugf("sendCheckAlive: Server is alive")
	return nil
}

// sendCreateBastion sends a create-bastion command to the server.
//
// Parameters:
//   - serverIP: IP address of the server
//   - cloudName: Name of the cloud configuration
//   - serverName: Name for the bastion server
//   - domainName: Domain name for the bastion
//
// Returns:
//   - error: Any error encountered during the operation
func sendCreateBastion(serverIP string, cloudName string, serverName string, domainName string) error {
	if serverIP == "" {
		return fmt.Errorf("server IP cannot be empty")
	}
	if cloudName == "" {
		return fmt.Errorf("cloud name cannot be empty")
	}
	if serverName == "" {
		return fmt.Errorf("server name cannot be empty")
	}
	if domainName == "" {
		return fmt.Errorf("domain name cannot be empty")
	}

	var (
		cmdIn          CommandCreateBastion
		cmdOut         CommandBastionCreated
		marshalledData []byte
		response       string
		err            error
	)

	cmdIn = CommandCreateBastion{
		Command:    serverCmdCreateBastion,
		CloudName:  cloudName,
		ServerName: serverName,
		DomainName: domainName,
	}

	log.Debugf("sendCreateBastion: Connecting to server at %s", serverIP)
	log.Debugf("sendCreateBastion: Creating bastion %s in cloud %s with domain %s", serverName, cloudName, domainName)

	// Use net.JoinHostPort to properly handle IPv6 addresses
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(serverIP, serverPort), 10 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to server %s:%s: %w", serverIP, serverPort, err)
	}
	defer conn.Close()

	marshalledData, err = json.Marshal(cmdIn)
	if err != nil {
		return fmt.Errorf("failed to marshal create-bastion command: %w", err)
	}
	log.Debugf("sendCreateBastion: Sending command: %s", string(marshalledData))

	// Send the command to the server
	err = sendByteArray(conn, marshalledData)
	if err != nil {
		return fmt.Errorf("failed to send create-bastion command: %w", err)
	}

	response, err = receiveResponse(conn)
	if err != nil {
		return fmt.Errorf("failed to receive response: %w", err)
	}
	log.Debugf("sendCreateBastion: Received response: %s", response)

	err = json.Unmarshal([]byte(response), &cmdOut)
	if err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}
	log.Debugf("sendCreateBastion: Parsed response: %+v", cmdOut)

	if cmdOut.Result != "" {
		return fmt.Errorf("server returned error: %s", cmdOut.Result)
	}

	log.Debugf("sendCreateBastion: Bastion created successfully")
	return nil
}

// sendMetadata sends cluster metadata to the server for creation or deletion.
//
// Parameters:
//   - metadataFile: Path to the metadata JSON file
//   - serverIP: IP address of the server
//   - shouldCreateMetadata: true to create metadata, false to delete
//
// Returns:
//   - error: Any error encountered during the operation
func sendMetadata(metadataFile string, serverIP string, shouldCreateMetadata bool) error {
	if metadataFile == "" {
		return fmt.Errorf("metadata file path cannot be empty")
	}
	if serverIP == "" {
		return fmt.Errorf("server IP cannot be empty")
	}

	var (
		content        []byte
		cmd            CommandSendMetadata
		marshalledData []byte
		err            error
	)

	log.Debugf("sendMetadata: Connecting to server at %s", serverIP)
	log.Debugf("sendMetadata: Reading metadata from %s", metadataFile)

	// Use net.JoinHostPort to properly handle IPv6 addresses
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(serverIP, serverPort), 10 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to server %s:%s: %w", serverIP, serverPort, err)
	}
	defer conn.Close()

	// Read metadata file (using os.ReadFile instead of deprecated ioutil.ReadFile)
	content, err = os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("failed to read metadata file %s: %w", metadataFile, err)
	}
	log.Debugf("sendMetadata: Read %d bytes from metadata file", len(content))

	// Create the command JSON structure
	if shouldCreateMetadata {
		cmd.Command = serverCmdCreateMetadata
		log.Debugf("sendMetadata: Creating metadata")
	} else {
		cmd.Command = serverCmdDeleteMetadata
		log.Debugf("sendMetadata: Deleting metadata")
	}

	err = json.Unmarshal(content, &cmd.Metadata)
	if err != nil {
		return fmt.Errorf("failed to unmarshal metadata from file: %w", err)
	}
	log.Debugf("sendMetadata: Parsed metadata: %+v", cmd)

	marshalledData, err = json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}
	log.Debugf("sendMetadata: Sending command: %s", string(marshalledData))

	// Send the command to the server
	err = sendByteArray(conn, marshalledData)
	if err != nil {
		return fmt.Errorf("failed to send metadata command: %w", err)
	}

	log.Debugf("sendMetadata: Metadata sent successfully")
	return nil
}
