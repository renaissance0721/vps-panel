package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/renaissance0721/vps-panel/panel/internal/localconnector"
	"golang.org/x/sys/windows"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	client := localconnector.NewClient()
	switch {
	case len(args) == 2 && args[0] == "pair":
		address, err := localconnector.PanelURL(args[1])
		if err != nil {
			return err
		}
		code, err := readPairingCode()
		if err != nil {
			return err
		}
		credential, err := client.Pair(ctx, address, code)
		if err != nil {
			return err
		}
		if err := saveProfile(profile{Panel: address, Credential: credential}); err != nil {
			return errors.New("无法保存配对凭据，请在 Panel 撤销此连接器后重新配对")
		}
		fmt.Println("配对成功。运行 vps-panel-local-connector.exe run 连接 Panel。")
		return nil
	case len(args) == 0 || (len(args) == 1 && args[0] == "run"):
		value, err := loadProfile()
		if err != nil {
			return errors.New("无法读取此 Windows 用户的配对凭据，请先运行 pair")
		}
		return client.Run(ctx, value.Panel, value.Credential, func(status string) { fmt.Println(status) })
	case len(args) == 1 && args[0] == "diagnose":
		ip, country, err := client.Diagnose(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("诊断站点看到的公网出口 IP 为 %s，地区为 %s。\n", ip, country)
		return nil
	case len(args) == 1 && (args[0] == "--help" || args[0] == "-h"):
		fmt.Println("用法：vps-panel-local-connector.exe pair <https://panel.example.com> | run | diagnose")
		return nil
	default:
		return errors.New("参数无效，请运行 vps-panel-local-connector.exe --help")
	}
}

func readPairingCode() (string, error) {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil || windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT) != nil {
		return "", errors.New("请在 Windows 终端内交互输入配对码")
	}
	defer windows.SetConsoleMode(handle, mode)
	fmt.Print("粘贴 Panel 一次性配对码，然后回车（输入不显示）：")
	value, err := bufio.NewReaderSize(os.Stdin, 256).ReadSlice('\n')
	fmt.Println()
	if err != nil || !localconnector.ValidToken(strings.TrimSpace(string(value))) {
		return "", errors.New("配对码格式无效")
	}
	return strings.TrimSpace(string(value)), nil
}
