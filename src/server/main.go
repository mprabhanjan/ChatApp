package main

import (
    "fmt"
    "google.golang.org/grpc"
    pb "grpcApi"
    "io"
    "log"
    "net"
    "sync"
)

const (
    grpcAddress = "localhost:9999"
)

type ChatRpcServerImpl struct {
    pb.UnimplementedChatRpcSrvcServer
    atomicClinentId  uint64
    clientMap  map[string]*Client
    server_ctrl_ch chan chan string
    client_chat_chnl chan *ChatMsgWrapper
    broadcaster_stop_chan chan struct{}
    wg *sync.WaitGroup
    mapMutex sync.Mutex
}

func (server *ChatRpcServerImpl) addClientToMap(client *Client) {
    server.mapMutex.Lock()
    server.clientMap[client.client_id] = client
    server.mapMutex.Unlock()
}

func (server *ChatRpcServerImpl) removeClientFromMap(client *Client) {
    server.mapMutex.Lock()
    delete(server.clientMap, client.client_id)
    server.mapMutex.Unlock()
}

type ChatMsgWrapper struct {
    msg *pb.ClientChatMessage
    from_client_name string
    from_client_id   string
}

func (server *ChatRpcServerImpl) getNewClientId(client_ch chan <- string) {
    server.atomicClinentId += 1
    new_client_id := fmt.Sprintf("|%016X|", server.atomicClinentId)
    log.Printf("Returned new Client Id %s", new_client_id)
    client_ch <- new_client_id
}

type Client struct {
    client_id string
    user_name string
    client_ch  chan *pb.ServerToClientMessage
    close_in_prog bool
    client_ctrl_ch chan string
    client_stream pb.ChatRpcSrvc_ChatServer
    rcv_stop_ch chan struct {}
    send_stop_ch chan struct {}
    wg *sync.WaitGroup
    server *ChatRpcServerImpl
}

func (rpcSrvr *ChatRpcServerImpl) Chat(stream pb.ChatRpcSrvc_ChatServer) error {

    log.Printf("Receiving new client request....")
    new_client := &Client {
        client_ctrl_ch: make(chan string, 0),
        client_stream: stream,
        rcv_stop_ch: make(chan struct{}, 0),
        send_stop_ch: make(chan struct{}, 0),
        wg: &sync.WaitGroup{},
        server: rpcSrvr,
        client_ch: make(chan *pb.ServerToClientMessage, 0),
    }

    new_client.server.server_ctrl_ch <- new_client.client_ctrl_ch
    new_client.client_id = <- new_client.client_ctrl_ch
    log.Printf("Received client_id = %s", new_client.client_id)

    new_client.wg.Add(1)
    go new_client.clientSendLoop()

    if err:= new_client.clientReceiveLoop(); err != nil {
        new_client.send_stop_ch <- struct{}{}
    }
    new_client.wg.Wait()
    return nil
}

func (client *Client) clientReceiveLoop() error {

    log.Printf("Client - Entering ReceiveLoop() ....")
    for {
        m, err := client.client_stream.Recv()
        if err == io.EOF {
            log.Printf("Client[Id:Name :: [%s:%s] Rcvd EOF from client side - %v",
                client.client_id, client.user_name, err)
            return err
        }
        if err != nil {
            log.Printf("Client[Id:Name :: %s:%s] Error in client receive - %v",
                client.client_id, client.user_name, err)
            return err
        }

        switch msg := m.ClientMessage.(type) {
        case *pb.ClientToServerMessage_HelloMsg:
            log.Printf("Received Client Hello Message")
            client.user_name = msg.HelloMsg.GetUserName()
            client.server.addClientToMap(client)

            helloAck := &pb.HelloAck{
                ServerVer: "Version_1",
                ClientId: client.client_id,
                OpaqueClientData: msg.HelloMsg.GetOpaqueClientData(),
                Https: msg.HelloMsg.GetHttps(),
            }
            serverAck := &pb.ServerToClientMessage{
                ServerMsg: &pb.ServerToClientMessage_HelloAck{HelloAck: helloAck},
            }
            client.client_ch <- serverAck

        case *pb.ClientToServerMessage_CloseMsg:
            log.Printf("Client [Id:Name::%s:%s] Rcvd Close!", client.client_id, client.user_name)
            client.server.removeClientFromMap(client)
            closeAck := &pb.ClientCloseAck{
                CloseAck: true,
                OpaqueClientData: msg.CloseMsg.GetOpaqueClientData(),
            }
            serverAck := &pb.ServerToClientMessage {
                ServerMsg: &pb.ServerToClientMessage_CloseAck{CloseAck: closeAck},
            }
            client.client_ch <- serverAck

        case *pb.ClientToServerMessage_NewMsg:
            log.Printf("Client [Id:Name::%s:%s] Rcvd Chat Msg!", client.client_id, client.user_name)
            chatAck := &pb.ClientChatMessageAck{
                MsgAck: true,
                OpaqueClientData: msg.NewMsg.GetOpaqueClientData(),
            }
            serverAck := &pb.ServerToClientMessage{
                ServerMsg: &pb.ServerToClientMessage_MsgAck{chatAck},
            }
            client.client_ch <- serverAck

            new_message := &ChatMsgWrapper{
                from_client_id:   client.client_id,
                from_client_name: client.user_name,
                msg: msg.NewMsg,
            }
            client.server.client_chat_chnl <- new_message
        }
    }

}

func (client *Client) clientSendLoop() {

    for {
        select {
        case <- client.send_stop_ch:
            log.Printf("Client Send-Side [Id:Name::%s:%s] Rcvd Stop!", client.client_id, client.user_name)
            return

        case msg := <- client.client_ch:
            log.Printf("Client Send Side [Id:Name::%s:%s] Sending a new message to client",
                client.client_id, client.user_name)
            err := client.client_stream.Send(msg)
            if err != nil {
                log.Printf("Client Send Side [Id:Name::%s:%s] Error Sending a message to client!! Error %v",
                    client.client_id, client.user_name, err)
                client.rcv_stop_ch <- struct{}{}
                client.wg.Done()
                return
            }
        }
    }
}

func main() {
    lis, err := net.Listen("tcp", grpcAddress)
    if err != nil {
        log.Fatalf("failed to listen: %v", err)
    }

    server := &ChatRpcServerImpl {
        clientMap: make(map[string]*Client),
        server_ctrl_ch: make(chan chan string, 128),
        client_chat_chnl: make(chan *ChatMsgWrapper, 128),
        broadcaster_stop_chan: make(chan struct{}, 0),
        wg: &sync.WaitGroup{},
    }
    log.Printf("Starting RPC Server....")
    s := grpc.NewServer()
    pb.RegisterChatRpcSrvcServer(s, server)
    log.Printf("Registered RPC Service at %s", grpcAddress)

    server.wg.Add(1)
    go server.broadcaster()

    go func () {
        for {
            select {
            case client_ch := <- server.server_ctrl_ch:
                server.getNewClientId(client_ch)
            }
        }
    }()

    if err := s.Serve(lis); err != nil {
        log.Fatalf("failed to serve: %v", err)
    }
}

func (server *ChatRpcServerImpl) broadcaster() {

    for {
        select {
        case client_chat_msg := <- server.client_chat_chnl:
            delivery_count := 0
            to_client_msg := &pb.ServerClientChatMessage{
                FromClient: client_chat_msg.from_client_name,
                Broadcast: client_chat_msg.msg.GetBroadcast(),
                Message: client_chat_msg.msg.GetMessageText(),
            }
            server_to_client_msg := &pb.ServerToClientMessage{
                ServerMsg: &pb.ServerToClientMessage_ClientMsg{ ClientMsg: to_client_msg, },
            }
            if !client_chat_msg.msg.Broadcast {
                // TBD
                continue
            }

            for cl_id, client := range server.clientMap {
                if client_chat_msg.from_client_id != "" && client_chat_msg.from_client_id != cl_id {
                    delivery_count += 1
                    client.client_ch <- server_to_client_msg
                }
            }
            log.Printf("Delivered msg from client %s to %d recepients.", to_client_msg.FromClient, delivery_count)

        case <- server.broadcaster_stop_chan:
            log.Printf("Broadcaster: Received on Stop Channel! Returning!!")
            server.wg.Done()
            return
        }
    }
}