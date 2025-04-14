package wecs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kubestellar/ui/k8s"
	"gopkg.in/igm/sockjs-go.v2/sockjs"
	"io"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog/v2"
	"net/http"
	"sync"
	"time"
)

const END_OF_TRANSMISSION = "\u0004"

// PtyHandler : pseudo-terminals (PTYs)
type PtyHandler interface {
	io.Reader
	io.Writer
	remotecommand.TerminalSizeQueue
}

type TerminalSession struct {
	id            string
	bound         chan error
	sizeChan      chan remotecommand.TerminalSize
	sockJSSession sockjs.Session
}

type TerminalMessage struct {
	Op, Data, SessionID string
	Rows, Cols          uint16
}

func (t TerminalSession) Next() *remotecommand.TerminalSize {
	size := <-t.sizeChan
	if size.Width == 0 && size.Height == 0 {
		return nil
	}
	return &size
}

func (t TerminalSession) Read(value []byte) (int, error) {
	m, err := t.sockJSSession.Recv()
	if err != nil {
		// Send terminated signal to process to avoid resource leak
		return copy(value, END_OF_TRANSMISSION), err
	}
	// convert to go struct types
	var msg TerminalMessage

	if err := json.Unmarshal([]byte(m), &msg); err != nil {
		return copy(value, END_OF_TRANSMISSION), err
	}

	switch msg.Op {
	case "stdin":
		return copy(value, msg.Data), nil
	case "resize":
		t.sizeChan <- remotecommand.TerminalSize{Width: msg.Cols, Height: msg.Rows}
		return 0, nil
	default:
		return copy(value, END_OF_TRANSMISSION), fmt.Errorf("unknown message type '%s'", msg.Op)
	}
}

func (t TerminalSession) Write(value []byte) (int, error) {
	msg, err := json.Marshal(TerminalMessage{
		Op:   "stdout",
		Data: string(value),
	})
	if err != nil {
		return 0, err
	}
	if err := t.sockJSSession.Send(string(msg)); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (t TerminalSession) Toast(value []byte) (int, error) {
	msg, err := json.Marshal(TerminalMessage{
		Op:   "toast",
		Data: string(value),
	})
	if err != nil {
		return 0, err
	}
	if err := t.sockJSSession.Send(string(msg)); err != nil {
		return 0, err
	}
	return len(value), nil
}

type SessionMap struct {
	Sessions map[string]TerminalSession
	Lock     sync.RWMutex
}

func (sm *SessionMap) Get(sessionId string) TerminalSession {
	sm.Lock.RLock()
	defer sm.Lock.RUnlock()
	return sm.Sessions[sessionId]
}
func (sm *SessionMap) Set(sessionId string, session TerminalSession) {
	sm.Lock.Lock()
	defer sm.Lock.Unlock()
	sm.Sessions[sessionId] = session
}

func (sm *SessionMap) Close(sessionId string, status uint32, reason string) {
	sm.Lock.Lock()
	defer sm.Lock.Unlock()
	session := sm.Sessions[sessionId]
	err := session.sockJSSession.Close(status, reason)
	if err != nil {
		klog.Error(err)
	}
	close(session.sizeChan)
	delete(sm.Sessions, sessionId)
}

var terminalSessions = SessionMap{Sessions: make(map[string]TerminalSession)}

func handleTerminalSession(session sockjs.Session) {
	var (
		buf             string
		err             error
		msg             TerminalMessage
		terminalSession TerminalSession
	)

	if buf, err = session.Recv(); err != nil {
		klog.Errorf("handleTerminalSession: can't Recv: %v", err)
		return
	}
	if err := json.Unmarshal([]byte(buf), &msg); err != nil {
		//klog.V(args.LogLevelVerbose).Infof("handleTerminalSession: expected 'bind' message, got: %s", buf)
		klog.Errorf("handleTerminalSession: expected 'bind' message, got: %s", buf)
		return
	}

	if terminalSession = terminalSessions.Get(msg.SessionID); terminalSession.id == "" {
		klog.Errorf("handleTerminalSession: can't find session '%s'", msg.SessionID)
		return
	}
	terminalSession.sockJSSession = session
	terminalSessions.Set(msg.SessionID, terminalSession)
	terminalSession.bound <- nil
}

func CreateAttachHandler(path string) http.Handler {
	return sockjs.NewHandler(path, sockjs.DefaultOptions, handleTerminalSession)
}
func genTerminalSessionId() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	id := make([]byte, hex.EncodedLen(len(bytes)))
	hex.Encode(id, bytes)
	return string(id), nil
}

func isValidShell(validShells []string, shell string) bool {
	for _, validShell := range validShells {
		if validShell == shell {
			return true
		}
	}
	return false
}

func startProcess(c *gin.Context, clientSet *kubernetes.Clientset, cfg *rest.Config, cmd []string, ptyHandler PtyHandler) error {
	namespace := c.Param("namespace")
	podName := c.Param("pod")
	containerName := c.Param("container")

	req := clientSet.CoreV1().RESTClient().Post().Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&v1.PodExecOptions{
		Container: containerName,
		Command:   cmd,
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", c.Request.URL)
	if err != nil {
		return err
	}
	err = exec.StreamWithContext(c, remotecommand.StreamOptions{
		Stdin:             ptyHandler,
		Stdout:            ptyHandler,
		Stderr:            ptyHandler,
		TerminalSizeQueue: ptyHandler,
		Tty:               true,
	})
	if err != nil {
		return err
	}

	return nil
}

func waitForTerminal(c *gin.Context, clientSet *kubernetes.Clientset, cfg *rest.Config, sessionId string) {
	shell := c.Query("shell")
	select {
	case <-terminalSessions.Get(sessionId).bound:
		close(terminalSessions.Get(sessionId).bound)
		var err error
		validShells := []string{"bash", "sh", "powershell", "cmd"}
		if isValidShell(validShells, shell) {
			cmd := []string{shell}
			err = startProcess(c, clientSet, cfg, cmd, terminalSessions.Get(sessionId))
		} else {
			for _, testShell := range validShells {
				cmd := []string{testShell}
				if err := startProcess(c, clientSet, cfg, cmd, terminalSessions.Get(sessionId)); err == nil {
					break
				}
			}
		}
		if err != nil {
			terminalSessions.Close(sessionId, 2, err.Error())
			return
		}
		terminalSessions.Close(sessionId, 1, "Process exited")

	case <-time.After(10 * time.Minute):
		close(terminalSessions.Get(sessionId).bound)
		delete(terminalSessions.Sessions, sessionId)
		return
	}
}

// /pod/{namespace}/{pod}/shell/{container}?context
func HandleExecShell(c *gin.Context) {
	sessionID, err := genTerminalSessionId()
	if err != nil {
		//
	}
	context := c.Query("context")
	if context == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "no context present as query",
		})
	}
	clientset, restConfig, err := k8s.GetClientSetWithConfigContext(context)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "error while setting the context",
		})
	}
	terminalSessions.Set(sessionID, TerminalSession{
		id:       sessionID,
		bound:    make(chan error),
		sizeChan: make(chan remotecommand.TerminalSize),
	})
	go waitForTerminal(c, clientset, restConfig, sessionID)
	c.JSON(http.StatusOK, gin.H{
		"id": sessionID,
	})
}
