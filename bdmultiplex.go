package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// This goroutine listens for callback connections and stores them for use on the control side
func callbackListener(port int, mutex *sync.Mutex, connections map[string]*net.Conn, connectionLocks map[string]bool, connectionChannels map[string]chan string) {

	server, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(port))

	if err != nil {
		fmt.Println("Ouch, callback port taken")
		return
	}

	defer server.Close()

	for {
		connection, _ := server.Accept()

		go func(conn net.Conn) {

			// Get ID for connection
			remoteID := conn.RemoteAddr().String()

			// TODO: Manage when connection already exists

			// Store a pointer to the connection and a channel from the connection
			mutex.Lock()
			connections[remoteID] = &conn
			connectionLocks[remoteID] = false
			connectionChannels[remoteID] = make(chan string)
			// This goroutine reads from the connection to a channel
			go func(pipeInConn net.Conn, out chan<- string) {
				reader := bufio.NewReader(pipeInConn)
				for {
					line, rerr := reader.ReadString('\n')
					if rerr == nil {
						out <- line
					} else {
						fmt.Println("died!")
						out <- "@@MOVED@@"
						return
					}
				}

			}((*connections[remoteID]), connectionChannels[remoteID])

			mutex.Unlock()

		}(connection)
	}
}

// This goroutine allows you to control and use callback connections.
func controlListener(port int, mutex *sync.Mutex, connections map[string]*net.Conn, connectionLocks map[string]bool, connectionChannels map[string]chan string) {

	KEY := os.Args[3]
	fmt.Println("'" + KEY + "'")

	server, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(port))

	if err != nil {
		fmt.Println("Ouch, control port taken")
		return
	}

	defer server.Close()

	for {
		connection, _ := server.Accept()

		go func(conn net.Conn) {

			defer conn.Close()

			var currentConn *net.Conn
			var currentID string

			auth := false

			reader := bufio.NewReader(conn)

			for {
				line, rerr := reader.ReadString('\n')

				if rerr == nil {

					// Check auth
					if strings.Trim(line, "\n") == KEY && !auth {
						auth = true
						conn.Write([]byte("Callback Multiplexer\n@help for help\n"))
					} else if !auth {
						conn.Write([]byte("HTTP/1.1 403 ACCESS FORBIDDEN\n"))
					} else if strings.HasPrefix(line, "@") {
						cmdArray := strings.Split(strings.Trim(line[1:], "\n"), " ")
						inCmd := cmdArray[0]

						response := "--"
						if inCmd == "list" {
							mutex.Lock()
							if len(connections) > 0 {
								response = ""
								for k := range connections {
									response += "   " + k + "\n"
								}
							} else {
								response = "No connection!\n"
							}
							mutex.Unlock()
						} else if inCmd == "conn" {
							if len(cmdArray) < 2 {
								response = "Usage: @conn <CONN>"
							} else {
								mutex.Lock()

								if value, exist := connections[cmdArray[1]]; exist {
									// Check if the connection is locked
									if connectionLocks[cmdArray[1]] == false {

										// Check if we have an existing connection
										if currentID != "" {
											if _, exist := connections[currentID]; exist {
												// Notify channel listeners to stop listening
												connectionChannels[currentID] <- "@@MOVED@@"
												// Unlock old connection
												connectionLocks[currentID] = false

												conn.Write([]byte("Disconnected from " + currentID + "\n"))
												currentID = ""
												currentConn = nil
											} else {
												conn.Write([]byte("Invalid current connection" + "\n"))
											}
										}

										connectionLocks[cmdArray[1]] = true
										currentID = cmdArray[1]
										currentConn = value
										response = "Connected to " + currentID

										// This goroutine reads from the connection channel
										go func(inChannel <-chan string, pipeOutConn net.Conn) {
											for {
												line := <-inChannel
												if !strings.HasPrefix(line, "@@MOVED@@") {
													pipeOutConn.Write([]byte(line))
												} else {
													fmt.Println("moved!")
													return
												}
											}
										}(connectionChannels[cmdArray[1]], conn)

									} else {
										response = "Somebody has locked this connection. Try again later."
									}
								} else {
									response = "Invalid connection"
								}
								mutex.Unlock()
							}
						} else if inCmd == "dis" {
							mutex.Lock()
							if currentID == "" {
								response = "No current session"
							} else {
								if _, exist := connections[currentID]; exist {
									connectionChannels[currentID] <- "@@MOVED@@"
									connectionLocks[currentID] = false
									currentID = ""
									currentConn = nil
									response = "Disconnected"
								} else {
									response = "Invalid connection"
								}
							}

							mutex.Unlock()

						} else if inCmd == "unlock" {
							response = "WARNING: This forces the unlock of a connection. Be sure nobody else is using this connection!\n"
							if len(cmdArray) == 2 {
								mutex.Lock()

								unlockCon := cmdArray[1]
								if _, exist := connections[unlockCon]; exist {
									connectionLocks[unlockCon] = false
									connectionChannels[unlockCon] <- "@@MOVED@@"
								} else {
									response = "Invalid connection: " + unlockCon
								}

								mutex.Unlock()
							} else {
								response = "Usage: @unlock [<CONN>]"

							}
						} else if inCmd == "w" {
							if currentID != "" {
								response = "Current connection: " + currentID
							} else {
								response = "No connection"
							}
						} else if inCmd == "help" {
							response = "HELP: \n"
							response += "* @list - list connections\n"
							response += "* @conn <CONN> - Use and lock to a callback connection\n"
							response += "* @dis - Detach and unlock a callback connection\n"
							response += "* @unlock <CONN> - Force unlock callback connection\n"
							response += "* @w - View current connection\n"
						} else {
							response = "Invalid command: " + inCmd
						}
						conn.Write([]byte(response + "\n"))
					} else if strings.Trim(line, "\n") == "exit" {
						conn.Write([]byte("Type EXIT to exit\n"))
					} else {
						// If we have a connection, write to it
						if currentConn != nil {
							_, err := (*currentConn).Write([]byte(line))
							// Check for errors in the connection
							if err != nil {
								conn.Write([]byte("Connection appears to be broken\n"))
								// Unlock the connection
								mutex.Lock()
								if _, exist := connections[currentID]; exist {

									(*currentConn).Close()
									delete(connections, currentID)
									currentConn = nil
									currentID = ""

								}
								mutex.Unlock()

							}
						} else {
							conn.Write([]byte("Type '@list' to list connections, type '@conn <CONN>' to connect to it. \n@help for help\n"))
						}

					}
				} else {
					// Control connection has dropped or has error
					fmt.Println("Control connection dropped")
					mutex.Lock()
					if _, exist := connections[currentID]; exist {
						connectionLocks[currentID] = false
					}
					currentConn = nil
					currentID = ""
					mutex.Unlock()
					return
				}
			}

		}(connection)
	}
}

func main() {

	if len(os.Args) < 4 {
		fmt.Println("Usage: multiple <CALLBACK_PORT> <CONTROL_PORT> <AUTH_KEY>")
		return
	}

	callbackPort, convErr := strconv.Atoi(os.Args[1])
	if convErr != nil {
		fmt.Println("Invalid callback port")
		return
	}

	controlPort, convErr := strconv.Atoi(os.Args[2])
	if convErr != nil {
		fmt.Println("Invalid control port")
		return
	}

	var listMutex sync.Mutex
	connections := make(map[string]*net.Conn)
	connectionLocks := make(map[string]bool)
	connectionChannels := make(map[string]chan string)

	go callbackListener(callbackPort, &listMutex, connections, connectionLocks, connectionChannels)
	go controlListener(controlPort, &listMutex, connections, connectionLocks, connectionChannels)
	for {
		// Endless loop!
	}
}
