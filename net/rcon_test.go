package net

import (
	"fmt"
	"testing"
)

func TestRCON(t *testing.T) {
	l, err := ListenRCON("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- serveRCON(l)
	}()

	resp, err := runRCONClient(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	want := `your command is "TEST COMMAND"`
	if resp != want {
		t.Fatalf("server response = %q, want %q", resp, want)
	}

	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func serveRCON(l *RCONListener) error {
	conn, err := l.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.AcceptLogin("RightPassword"); err != nil {
		return fmt.Errorf("accept login: %w", err)
	}

	cmd, err := conn.AcceptCmd()
	if err != nil {
		return fmt.Errorf("accept command: %w", err)
	}

	resp := handleCommand(cmd)
	if err := conn.RespCmd(resp); err != nil {
		return fmt.Errorf("respond to command: %w", err)
	}
	return nil
}

func handleCommand(cmd string) (resp string) {
	return fmt.Sprintf("your command is %q", cmd)
}

func runRCONClient(addr string) (string, error) {
	conn, err := DialRCON(addr, "RightPassword")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if err := conn.Cmd("TEST COMMAND"); err != nil {
		return "", err
	}

	resp, err := conn.Resp()
	if err != nil {
		return "", err
	}
	return resp, nil
}

func ExampleListenRCON() {
	l, err := ListenRCON("localhost:25575")
	if err != nil {
		panic(err)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Accept connection error: %v", err)
		}

		go func(conn RCONServerConn) {
			err = conn.AcceptLogin("CORRECT_PASSWORD")
			if err != nil {
				fmt.Printf("Login fail: %v", err)
			}
			defer conn.Close()

			// The client is login, we are accepting its command
			for {
				cmd, err := conn.AcceptCmd()
				if err != nil {
					fmt.Printf("Read command fail: %v", err)
					break
				}

				resp := handleCommand(cmd)

				// Return the result of command.
				// It's allowed to call RespCmd multiple times for one command.
				err = conn.RespCmd(resp)
				if err != nil {
					fmt.Printf("Response command fail: %v", err)
					break
				}
			}
		}(conn)
	}
}

func ExampleDialRCON() {
	conn, err := DialRCON("localhost:25575", "CORRECT_PASSWORD")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	err = conn.Cmd("TEST COMMAND")
	if err != nil {
		panic(err)
	}

	for {
		// Server may send the result in more(or less) than one packet.
		// See: https://wiki.vg/RCON#Fragmentation
		resp, err := conn.Resp()
		if err != nil {
			fmt.Print(err)
		}
		fmt.Printf("Server response: %q", resp)
		break
	}
}
