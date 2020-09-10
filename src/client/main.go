package main

import (
    "bufio"
    "bytes"
    "context"
    "fmt"
    "google.golang.org/grpc"
    "io"
    "log"
    "os"
    "time"
    pb "grpcApi"
)

const (
    grpcAddress = "localhost:9999"
)

func main() {

    // Set up a connection to the server.
    conn, err := grpc.Dial(grpcAddress, grpc.WithInsecure(), grpc.WithBlock())
    if err != nil {
        log.Fatalf("did not connect: %v", err)
    }
    defer conn.Close()
    c := pb.NewChatRpcSrvcClient(conn)

    // Contact the server and print out its response.
    //ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
    //defer cancel()

    //srvrStm, err := c.Chat(ctx)
    srvrStm, err := c.Chat(context.Background())
    if err != nil {
        log.Fatalf("could not establish Chat channel: %v", err)
    }

    stopChan := make(chan struct{}, 0)
    go receiveFromServer(srvrStm, stopChan)

    helloMsg := &pb.Hello{
        ClientVer: "Version_1",
        Https:     false,
    }
    clientHello := &pb.ClientToServerMessage{
        ClientMessage: &pb.ClientToServerMessage_HelloMsg{HelloMsg: helloMsg},
    }
    err = srvrStm.Send(clientHello)
    if err != nil {
        log.Fatalf("Unable to send Initial Hello Message to Chat Server!! Error = %v", err)
        return
    }

    go readFromUser(os.Stdin, srvrStm, stopChan)

    <- stopChan
    log.Printf("Received Stop Signal. Stopping the Client App!!")
    return
}

func receiveFromServer(srvrStm pb.ChatRpcSrvc_ChatClient, stopChan chan struct{}) {

    sendStop := false
    for {
        respMsg, err := srvrStm.Recv()
        if err == io.EOF {
            log.Print("Server closed the connection!")
            stopChan <- struct{}{}
            return
        }
        if err != nil {
            log.Print("cannot receive stream response!!: %v", err)
            stopChan <- struct{}{}
            return
        }

        var buf bytes.Buffer
        switch msg := respMsg.ServerMsg.(type) {
        case *pb.ServerToClientMessage_HelloAck:
            helloAck := msg.HelloAck
            fmt.Fprint(&buf, "MsgType: HelloAck, Server-Ver %s, Client_Id: %s",
                helloAck.GetServerVer(), helloAck.GetClientId())

        case *pb.ServerToClientMessage_MsgAck:
            msgAck := msg.MsgAck
            ackStr := ""
            if msgAck.GetMsgAck() == true {
                ackStr = "ACK"
            } else {
                ackStr = "NACK"
            }
            fmt.Fprint(&buf, "MsgType: MsgAck, Status: %s", ackStr)

        case *pb.ServerToClientMessage_CloseAck:
            msgAck := msg.CloseAck
            ackStr := ""
            if msgAck.GetCloseAck() == true {
                ackStr = "ACK"
            } else {
                ackStr = "NACK"
            }
            fmt.Fprint(&buf, "MsgType: CloseAck, Status: %s", ackStr)
            sendStop = true

        case *pb.ServerToClientMessage_ClientMsg:
            chatMsg := msg.ClientMsg
            bcast := ""
            if chatMsg.GetBroadcast() {
                bcast = "B"
            }
            fmt.Fprint(&buf, "Client Msg from %s(%s): %s", chatMsg.GetFromClient(), bcast, chatMsg.GetMessage())
        }

        //Send to Stdout
        CopyStdInOut(os.Stdout, &buf)
        if sendStop {
            time.Sleep(1 * time.Second)
            stopChan <- struct{}{}
            return
        }
    }
}


func CopyStdInOut(dst io.Writer, src io.Reader) {
    if _, err := io.Copy(dst, src); err != nil {
        log.Fatal(err)
    }
}

func readFromUser(src io.Reader, srvrStm pb.ChatRpcSrvc_ChatClient, stopChan chan struct{}) {
    scanner := bufio.NewScanner(src)
    for scanner.Scan() {
        log.Printf("Scanned msg to be sent: %s", scanner.Text())
        userMsg := &pb.ClientChatMessage{
            Broadcast: true,
            MessageText: scanner.Text(),
        }
        clientMsg := &pb.ClientToServerMessage{
            ClientMessage: &pb.ClientToServerMessage_NewMsg{NewMsg: userMsg},
        }
        err := srvrStm.Send(clientMsg)
        if err != nil {
            log.Printf("Unable to send Client Message to Chat Server!! Error = %v", err)
            stopChan <- struct{}{}
            return
        }
    }
    if err := scanner.Err(); err != nil {
        log.Printf("Error reading standard input: %v", err)
        stopChan <- struct{}{}
        return
    }
}