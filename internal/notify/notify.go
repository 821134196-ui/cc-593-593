// Package notify 模拟寄送通知接口：真实环境对接短信/快递平台，这里写日志文件并打印到控制台。
package notify

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type Notifier struct {
	logPath string
	mu      sync.Mutex
}

func New(logPath string) *Notifier {
	return &Notifier{logPath: logPath}
}

// SendShippingNotice 模拟向失主发送寄送通知
func (n *Notifier) SendShippingNotice(recipient, phone, bagNo, trackingNo, address string) error {
	line := fmt.Sprintf("[%s] 寄送通知 -> 收件人:%s 电话:%s 封袋号:%s 快递单号:%s 地址:%s\n",
		time.Now().Format(time.RFC3339), recipient, phone, bagNo, trackingNo, address)

	n.mu.Lock()
	defer n.mu.Unlock()
	f, err := os.OpenFile(n.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	fmt.Print("[mock-notify] " + line)
	return nil
}
