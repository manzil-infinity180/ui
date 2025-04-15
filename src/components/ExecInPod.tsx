import React, { useRef, useState, useEffect, useCallback } from 'react';
import SockJS from 'sockjs-client';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import debounce from 'lodash.debounce';
import 'xterm/css/xterm.css';

interface TerminalMessage {
    Op: string;
    Data?: string;
    SessionID?: string;
    Cols?: number;
    Rows?: number;
}

const ExecInPod: React.FC = () => {
    const [conn, setConn] = useState<WebSocket | null>(null);
    const [sessionId, setSessionId] = useState<string | null>(null);
    const termRef = useRef<Terminal | null>(null);
    const fitAddonRef = useRef<FitAddon | null>(null);
    const anchorRef = useRef<HTMLDivElement>(null);
    const [isConnecting, setIsConnecting] = useState(false);

    // Clean up resources when component unmounts
    useEffect(() => {
        return () => {
            if (conn) {
                conn.close();
            }
            if (termRef.current) {
                termRef.current.dispose();
            }
        };
    }, []);

    const setupTerminal = useCallback(() => {
        if (termRef.current) {
            termRef.current.dispose();
        }

        const term = new Terminal({
            fontSize: 14,
            cursorBlink: true,
            theme: {
                background: '#000000',
                foreground: '#ffffff'
            }
        });

        const fitAddon = new FitAddon();
        term.loadAddon(fitAddon);

        if (anchorRef.current) {
            term.open(anchorRef.current);
            setTimeout(() => {
                fitAddon.fit();
            }, 100);
        }

        termRef.current = term;
        fitAddonRef.current = fitAddon;

        const resizeHandler = debounce(() => {
            if (fitAddonRef.current) {
                fitAddonRef.current.fit();
                const { cols, rows } = termRef.current!;
                if (conn && conn.readyState === WebSocket.OPEN) {
                    const msg: TerminalMessage = {
                        Op: 'resize',
                        Cols: cols,
                        Rows: rows
                    };
                    conn.send(JSON.stringify(msg));
                }
            }
        }, 100);

        window.addEventListener('resize', resizeHandler);

        return () => {
            window.removeEventListener('resize', resizeHandler);
        };
    }, [conn]);

    // Terminal input handler
    useEffect(() => {
        if (!termRef.current || !conn) return;

        const handleInput = (data: string) => {
            if (conn && conn.readyState === WebSocket.OPEN) {
                const msg: TerminalMessage = { Op: 'stdin', Data: data };
                conn.send(JSON.stringify(msg));
            }
        };

        termRef.current.onData(handleInput);

        return () => {
            // Cleanup listener on rerender
            if (termRef.current) {
                // No direct way to remove onData listeners in xterm,
                // but it will be disposed when we create a new terminal
            }
        };
    }, [conn, termRef.current]);

    // Terminal resize handler
    useEffect(() => {
        if (!termRef.current || !conn) return;

        const handleResize = (size: { cols: number; rows: number }) => {
            if (conn && conn.readyState === WebSocket.OPEN) {
                const msg: TerminalMessage = {
                    Op: 'resize',
                    Cols: size.cols,
                    Rows: size.rows
                };
                conn.send(JSON.stringify(msg));
            }
        };

        termRef.current.onResize(handleResize);

        // Send initial size
        // if (termRef.current.cols && termRef.current.rows) {
        //     const msg: TerminalMessage = {
        //         Op: 'resize',
        //         Cols: termRef.current.cols,
        //         Rows: termRef.current.rows
        //     };
        //     conn.send(JSON.stringify(msg));
        // }

        return () => {
            // Cleanup
        };
    }, [conn, termRef.current]);

    const connectToWebSocket = useCallback((id: string) => {
        // Create a proper SockJS connection with the appropriate path
        // Make sure this matches your backend SockJS handler path
        const socket = new SockJS(`http://localhost:4000/api/sockjs?${id}`); // This should match CreateSockjsAttachHandler path

        socket.onopen = () => {
            if (termRef.current) {
                termRef.current.writeln('\r\nWebSocket connected! Binding session...');

                // Send initial bind message
                const bindMsg: TerminalMessage = {
                    Op: 'bind',
                    SessionID: id,
                };
                socket.send(JSON.stringify(bindMsg));

                // Focus the terminal
                termRef.current.focus();
            }
        };

        socket.onmessage = (e: MessageEvent) => {
            if (termRef.current) {
                try {
                    const msg: TerminalMessage = JSON.parse(e.data);

                    if (msg.Op === 'stdout' && msg.Data) {
                        termRef.current.write(msg.Data);
                    } else if (msg.Op === 'toast' && msg.Data) {
                        termRef.current.writeln(`\r\n[System]: ${msg.Data}\r\n`);
                    }
                } catch (err) {
                    console.error('Error processing message:', err);
                    termRef.current.writeln('\r\nError processing message from server');
                }
            }
        };

        socket.onclose = (e) => {
            if (termRef.current) {
                termRef.current.writeln(`\r\n\r\nConnection closed: ${e.reason || 'Unknown reason'}`);
            }
            setConn(null);
            setSessionId(null);
            setIsConnecting(false);
        };

        socket.onerror = (e) => {
            if (termRef.current) {
                termRef.current.writeln('\r\n\r\nWebSocket error occurred');
            }
            console.error('WebSocket error:', e);
            setIsConnecting(false);
        };

        setConn(socket);

    }, []);

    const handleExec = useCallback(async () => {
        if (isConnecting) return;

        // Close existing connection if any
        if (conn) {
            conn.close();
            setConn(null);
            setSessionId(null);
        }

        setIsConnecting(true);

        try {
            // Setup the terminal first
            setupTerminal();

            if (!termRef.current) {
                console.error("Terminal not initialized");
                setIsConnecting(false);
                return;
            }

            termRef.current.clear();
            termRef.current.writeln('Connecting to pod...');

            // Request a new session from the backend
            const response = await fetch(
                `http://localhost:4000/api/v1/pod/nginx-1/nginx-singleton-deployment-6c644f6bd9-47zcs/shell/nginx?context=cluster1&shell=bash`
            );

            if (!response.ok) {
                const error = await response.text();
                termRef.current.writeln(`\r\nError: ${error}`);
                setIsConnecting(false);
                return;
            }

        try{
            const data = await response.json();
            console.log(data)
            if (!data.id) {
                termRef.current.writeln('\r\nError: No session ID returned from server');
                setIsConnecting(false);
                return;
            }

            termRef.current.writeln(`\r\nSession ID: ${data.id}`);
            setSessionId(data.id);

            // Connect to the WebSocket with the session ID
            connectToWebSocket(data.id);
        } catch (parseError) {
            termRef.current.writeln(`\r\nError parsing response as JSON:`);
                console.log(parseError)
            setIsConnecting(false);
        }

        } catch (error) {
            console.error('Error in handleExec:', error);
            if (termRef.current) {
                termRef.current.writeln(`\r\n\r\nError: ${error instanceof Error ? error.message : String(error)}`);
            }
            setIsConnecting(false);
        }
    }, [setupTerminal, connectToWebSocket, conn, isConnecting]);

    return (
        <div className="p-4">
            <div className="mb-4 flex items-center">
                <button
                    onClick={handleExec}
                    disabled={isConnecting}
                    className={`px-4 py-2 rounded transition ${
                        isConnecting
                            ? 'bg-gray-400 cursor-not-allowed'
                            : 'bg-blue-600 hover:bg-blue-700 text-white'
                    }`}
                >
                    {isConnecting ? 'Connecting...' : 'Connect to Pod Shell'}
                </button>
                <span className="ml-4 text-sm text-gray-500">
                    {sessionId ? `Connected to session: ${sessionId.substring(0, 8)}...` : 'nginx in nginx-singleton-deployment'}
                </span>
            </div>

            <div
                ref={anchorRef}
                className="w-full h-[500px] bg-black border border-gray-700 rounded overflow-hidden"
            />
        </div>
    );
};

export default ExecInPod;