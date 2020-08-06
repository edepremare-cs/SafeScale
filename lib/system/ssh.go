/*
 * Copyright 2018-2020, CS Systemes d'Information, http://www.c-s.fr
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package system

import (
    "bytes"
    "crypto/rand"
    "crypto/rsa"
    "crypto/x509"
    "encoding/pem"
    "fmt"
    "io"
    "io/ioutil"
    "net"
    "os"
    "os/exec"
    "reflect"
    "runtime"
    "strconv"
    "strings"
    "text/template"
    "time"

    "github.com/sirupsen/logrus"
    "golang.org/x/crypto/ssh"

    "github.com/CS-SI/SafeScale/lib/utils"
    "github.com/CS-SI/SafeScale/lib/utils/cli"
    "github.com/CS-SI/SafeScale/lib/utils/cli/enums/outputs"
    "github.com/CS-SI/SafeScale/lib/utils/concurrency"
    "github.com/CS-SI/SafeScale/lib/utils/data"
    "github.com/CS-SI/SafeScale/lib/utils/debug"
    "github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
    "github.com/CS-SI/SafeScale/lib/utils/fail"
    "github.com/CS-SI/SafeScale/lib/utils/retry"
    "github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// VPL: SSH ControlMaster options: -oControlMaster=auto -oControlPath=/tmp/safescale-%C -oControlPersist=5m
//      To make profit of this multiplexing functionality, we have to change the way we manage ports for tunnels: we have to always
//      use the same port for all access to a same host (not the case currently)
//      May not be used for interactive ssh connection...
const sshOptions = "-q -oIdentitiesOnly=yes -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oPubkeyAuthentication=yes -oPasswordAuthentication=no"

var (
    sshErrorMap = map[int]string{
        1:  "Malformed configuration or invalid cli options",
        2:  "Connection failed",
        65: "Host not allowed to connect",
        66: "General error in ssh protocol",
        67: "Key exchange failed",
        69: "MAC error",
        70: "Compression error",
        71: "Service not available",
        72: "Protocol version not supported",
        73: "Host key not verifiable",
        74: "Connection failed",
        75: "Disconnected by application",
        76: "Too many connections",
        77: "Authentication cancelled by user",
        78: "No more authentication methods available",
        79: "Invalid user name",
    }
    scpErrorMap = map[int]string{
        1:  "General error in file copy",
        2:  "Destination is not directory, but it should be",
        3:  "Maximum symlink level exceeded",
        4:  "Connecting to host failed",
        5:  "Connection broken",
        6:  "File does not exist",
        7:  "No permission to access file",
        8:  "General error in sftp protocol",
        9:  "File transfer protocol mismatch",
        10: "No file matches a given criteria",
        65: "Host not allowed to connect",
        66: "General error in ssh protocol",
        67: "Key exchange failed",
        69: "MAC error",
        70: "Compression error",
        71: "Service not available",
        72: "Protocol version not supported",
        73: "Host key not verifiable",
        74: "Connection failed",
        75: "Disconnected by application",
        76: "Too many connections",
        77: "Authentication cancelled by user",
        78: "No more authentication methods available",
        79: "Invalid user name",
    }
)

// IsSSHRetryable tells if the retcode of a ssh command may be retried
func IsSSHRetryable(code int) bool {
    if code == 2 || code == 4 || code == 5 || code == 66 || code == 67 || code == 70 || code == 74 || code == 75 || code == 76 {
        return true
    }
    return false

}

// IsSCPRetryable tells if the retcode of a scp command may be retried
func IsSCPRetryable(code int) bool {
    if code == 4 || code == 5 || code == 66 || code == 67 || code == 70 || code == 74 || code == 75 || code == 76 {
        return true
    }
    return false
}

// SSHConfig helper to manage ssh session
type SSHConfig struct {
    User                   string
    Host                   string
    PrivateKey             string
    Port                   int
    LocalPort              int
    GatewayConfig          *SSHConfig
    SecondaryGatewayConfig *SSHConfig
    // cmdTpl                 string
}

// IsNull tells if the instance is a null value
func (sc *SSHConfig) IsNull() bool {
    return sc == nil || sc.Host == ""
}

// SSHTunnel a SSH tunnel
type SSHTunnel struct {
    port      int
    cmd       *exec.Cmd
    cmdString string
    keyFile   *os.File
}

// SSHErrorString returns if possible the string corresponding to SSH execution
func SSHErrorString(retcode int) string {
    if msg, ok := sshErrorMap[retcode]; ok {
        return msg
    }
    return "Unqualified error"
}

// SCPErrorString returns if possible the string corresponding to SCP execution
func SCPErrorString(retcode int) string {
    if msg, ok := scpErrorMap[retcode]; ok {
        return msg
    }
    return "Unqualified error"
}

// Close closes ssh tunnel
func (tunnel *SSHTunnel) Close() fail.Error {
    defer func() {
        lazyErr := utils.LazyRemove(tunnel.keyFile.Name())
        if lazyErr != nil {
            logrus.Error(lazyErr)
        }
    }()

    // Kills the process of the tunnel
    err := tunnel.cmd.Process.Kill()
    if err != nil {
        logrus.Errorf("tunnel.cmd.Process.Kill() failed: %s", reflect.TypeOf(err).String())
        return fail.Wrap(err, "unable to close tunnel")
    }
    // Kills remaining processes if there are some
    bytesCmd, err := exec.Command("pgrep", "-f", tunnel.cmdString).Output()
    if err == nil {
        portStr := strings.Trim(string(bytesCmd), "\n")
        _, err = strconv.Atoi(portStr)
        if err == nil {
            err = exec.Command("kill", "-9", portStr).Run()
            if err != nil {
                logrus.Errorf("kill -9 failed: %s", reflect.TypeOf(err).String())
                return fail.Wrap(err, "unable to close tunnel")
            }
        }
    }
    return nil
}

// GetFreePort get a free port
func getFreePort() (int, fail.Error) {
    listener, err := net.Listen("tcp", ":0")
    defer func() {
        clErr := listener.Close()
        if clErr != nil {
            logrus.Error(clErr)
        }
    }()
    if err != nil {
        return 0, fail.NewError(err.Error())
    }
    tcp, ok := listener.Addr().(*net.TCPAddr)
    if !ok {
        return 0, fail.NewError("invalid listener.Addr()")
    }

    port := tcp.Port
    return port, nil
}

// CreateTempFileFromString creates a temporary file containing 'content'
func CreateTempFileFromString(content string, filemode os.FileMode) (*os.File, fail.Error) {
    defaultTmpDir := "/tmp"
    if runtime.GOOS == "windows" {
        defaultTmpDir = ""
    }

    f, err := ioutil.TempFile(defaultTmpDir, "") // TODO: Windows friendly
    if err != nil {
        return nil, fail.ExecutionError(err, "failed to create temporary file")
    }
    _, err = f.WriteString(content)
    if err != nil {
        logrus.Warnf("Error writing string: %v", err)
        return nil, fail.ExecutionError(err, "failed to wrote string to temporary file")
    }

    err = f.Chmod(filemode)
    if err != nil {
        logrus.Warnf("Error changing directory: %v", err)
        return nil, fail.ExecutionError(err, "failed to change temporary file acess rights")
    }

    err = f.Close()
    if err != nil {
        logrus.Warnf("Error closing file: %v", err)
        return nil, fail.ExecutionError(err, "failed to close temporary file")
    }

    return f, nil
}

func isTunnelReady(port int) bool {
    // Try to create a server with the port
    server, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
    if err != nil {
        return true
    }
    err = server.Close()
    if err != nil {
        logrus.Warnf("Error closing server: %v", err)
    }
    return false

}

// buildTunnel create SSH from local host to remote host through gateway
// if localPort is set to 0 then it's  automatically choosed
func buildTunnel(cfg *SSHConfig) (*SSHTunnel, fail.Error) {
    f, err := CreateTempFileFromString(cfg.GatewayConfig.PrivateKey, 0400)
    if err != nil {
        return nil, err
    }
    localPort := cfg.LocalPort
    if localPort == 0 {
        localPort, err = getFreePort()
        if err != nil {
            return nil, err
        }
    }

    options := sshOptions + " -oServerAliveInterval=60"
    cmdString := fmt.Sprintf("ssh -i %s -NL 127.0.0.1:%d:%s:%d %s@%s %s -p %d",
        f.Name(),
        localPort,
        cfg.Host,
        cfg.Port,
        cfg.GatewayConfig.User,
        cfg.GatewayConfig.Host,
        options,
        cfg.GatewayConfig.Port,
    )
    cmd := exec.Command("sh", "-c", cmdString)
    cerr := cmd.Start()
    //	err = cmd.Wait()
    if cerr != nil {
        return nil, fail.ToError(cerr)
    }

    /*
    	if forensics := os.Getenv("SAFESCALE_FORENSICS"); forensics != "" {
    		if cmdString != "" {
    			logrus.Debugf("[TRACE] %s", cmdString)
    		}
    		_ = os.MkdirAll(utils.AbsPathify(fmt.Sprintf("$HOME/.safescale/forensics/%s", cfg.Host)), 0777)
    		partials := strings.Split(f.Name(), "/")
    		dumpName := utils.AbsPathify(fmt.Sprintf("$HOME/.safescale/forensics/%s/%s.sshkey", cfg.Host, partials[len(partials)-1]))
    		err = ioutil.WriteFile(dumpName, []byte(cfg.GatewayConfig.PrivateKey), 0644)
    		if err != nil {
    			logrus.Warnf("[TRACE] Failure storing key in %s", dumpName)
    		}
    	}
    */

    for nbiter := 0; !isTunnelReady(localPort) && nbiter < 100; nbiter++ {
        time.Sleep(10 * time.Millisecond)
    }
    return &SSHTunnel{
        port:      localPort,
        cmd:       cmd,
        cmdString: cmdString,
        keyFile:   f,
    }, nil
}

// SSHCommand defines a SSH command
type SSHCommand struct {
    cmd     *exec.Cmd
    tunnels []*SSHTunnel
    keyFile *os.File
}

func (sc *SSHCommand) closeTunneling() fail.Error {
    var err fail.Error
    for _, t := range sc.tunnels {
        err = t.Close()
    }
    sc.tunnels = []*SSHTunnel{}

    // Tunnels are imbricated only last error is significant
    if err != nil {
        logrus.Errorf("closeTunneling: %s", reflect.TypeOf(err).String())
    }

    return err
}

// Wait waits for the command to exit and waits for any copying to stdin or copying from stdout or stderr to complete.
// The command must have been started by Start.
// The returned error is nil if the command runs, has no problems copying stdin, stdout, and stderr, and exits with a zero exit status.
// If the command fails to run or doesn't complete successfully, the error is of type *ExitError. Other error types may be returned for I/O problems.
// Wait also waits for the I/O loop copying from c.Stdin into the process's standard input to complete.
// Wait releases any resources associated with the cmd.
// Note: the error returned is not using fail.Error because we may need to cast the error to recover return code
func (sc *SSHCommand) Wait() error {
    err := sc.cmd.Wait()
    nerr := sc.cleanup()
    if err != nil {
        return err
    }
    if nerr != nil {
        logrus.Warnf("Error waiting for command cleanup: %v", nerr)
    }
    return nerr
}

// Kill kills SSHCommand process and releases any resources associated with the SSHCommand.
func (sc *SSHCommand) Kill() fail.Error {
    err := sc.cmd.Process.Kill()
    if err != nil {
        return fail.ToError(err)
    }
    return nil
}

// StdoutPipe returns a pipe that will be connected to the command's standard output when the command starts.
// Wait will close the pipe after seeing the command exit, so most callers need not close the pipe themselves; however, an implication is that it is incorrect to call Wait before all reads from the pipe have completed.
// For the same reason, it is incorrect to call Run when using StdoutPipe.
func (sc *SSHCommand) StdoutPipe() (io.ReadCloser, fail.Error) {
    r, err := sc.cmd.StdoutPipe()
    if err != nil {
        return nil, fail.NewError(err.Error())
    }
    return r, nil
}

// StderrPipe returns a pipe that will be connected to the command's standard error when the command starts.
// Wait will close the pipe after seeing the command exit, so most callers need not close the pipe themselves; however, an implication is that it is incorrect to call Wait before all reads from the pipe have completed. For the same reason, it is incorrect to use Run when using StderrPipe.
func (sc *SSHCommand) StderrPipe() (io.ReadCloser, fail.Error) {
    r, err := sc.cmd.StderrPipe()
    if err != nil {
        return nil, fail.NewError(err.Error())
    }
    return r, nil
}

// StdinPipe returns a pipe that will be connected to the command's standard input when the command starts.
// The pipe will be closed automatically after Wait sees the command exit.
// A caller need only call Close to force the pipe to close sooner.
// For example, if the command being run will not exit until standard input is closed, the caller must close the pipe.
func (sc *SSHCommand) StdinPipe() (io.WriteCloser, fail.Error) {
    r, err := sc.cmd.StdinPipe()
    if err != nil {
        return nil, fail.NewError(err.Error())
    }
    return r, nil
}

// Output runs the command and returns its standard output.
// Any returned error will usually be of type *ExitError.
// If c.Stderr was nil, Output populates ExitError.Stderr.
func (sc *SSHCommand) Output() ([]byte, fail.Error) {
    content, err := sc.cmd.Output()
    nerr := sc.cleanup()
    if err != nil {
        return nil, fail.NewError(err.Error())
    }
    if nerr != nil {
        logrus.Warnf("Error waiting for command cleanup: %v", nerr)
    }
    return content, nil
}

// CombinedOutput runs the command and returns its combined standard
// output and standard error.
func (sc *SSHCommand) CombinedOutput() ([]byte, fail.Error) {
    content, err := sc.cmd.CombinedOutput()
    nerr := sc.cleanup()
    if err != nil {
        return nil, fail.NewError(err.Error())
    }
    if nerr != nil {
        logrus.Warnf("Error waiting for command cleanup: %v", nerr)
    }
    return content, nil
}

// Start starts the specified command but does not wait for it to complete.
//
// The Wait method will return the exit code and release associated resources
// once the command exits.
func (sc *SSHCommand) Start() fail.Error {
    err := sc.cmd.Start()
    if err != nil {
        return fail.NewError(err.Error())
    }
    return nil
}

// Display ...
func (sc *SSHCommand) Display() string {
    return strings.Join(sc.cmd.Args, " ")
}

// Run starts the specified command and waits for it to complete.
//
// The returned error is nil if the command runs, has no problems
// copying stdin, stdout, and stderr, and exits with a zero exit
// status.
//
// If the command starts but does not complete successfully, the error is of
// type *ExitError. Other error types may be returned for other situations.
//
// WARNING : This function CAN lock, use .RunWithTimeout instead
func (sc *SSHCommand) Run(t concurrency.Task, outs outputs.Enum) (int, string, string, fail.Error) {
    tracer := debug.NewTracer(t, false, "(%s)", outs.String()).WithStopwatch().Entering()
    defer tracer.Exiting()

    return sc.RunWithTimeout(t, outs, 0)
}

// RunWithTimeout ...
func (sc *SSHCommand) RunWithTimeout(task concurrency.Task, outs outputs.Enum, timeout time.Duration) (int, string, string, fail.Error) {
    tracer := debug.NewTracer(task, tracing.ShouldTrace("ssh"), "(%s, %v)", outs.String(), timeout).WithStopwatch().Entering()
    tracer.Trace("command=\n%s\n", sc.Display())
    defer tracer.Exiting()
    // Set up the outputs (std and err)
    stdoutPipe, err := sc.StdoutPipe()
    if err != nil {
        return 0, "", "", err
    }
    stderrPipe, err := sc.StderrPipe()
    if err != nil {
        return 0, "", "", err
    }

    subtask, err := concurrency.NewTaskWithParent(task)
    if err != nil {
        return -1, "", "", err
    }
    _, err = subtask.StartWithTimeout(sc.taskExecute, data.Map{
        "stdout":          stdoutPipe,
        "stderr":          stderrPipe,
        "collect_outputs": outs != outputs.DISPLAY,
    }, timeout)
    if err != nil {
        return -1, "", "", err
    }

    r, err := subtask.Wait()
    if err != nil {
        return -1, "", "", err
    }
    if result, ok := r.(data.Map); ok {
        return result["retcode"].(int), result["stdout"].(string), result["stderr"].(string), nil
    }
    return -1, "", "", fail.InconsistentError("'result' should have been of type 'data.Map'")
}

func (sc *SSHCommand) taskExecute(task concurrency.Task, p concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
    if sc == nil {
        return nil, fail.InvalidInstanceError()
    }
    if task.IsNull() {
        return nil, fail.InvalidParameterError("task", "cannot be nil")
    }

    params, ok := p.(data.Map)
    if !ok {
        return nil, fail.InvalidParameterError("p", "must be of type data.Map")
    }
    var (
        stdoutPipe, stderrPipe io.ReadCloser
        collectOutputs         bool
    )
    stdoutPipe, ok = params["stdout"].(io.ReadCloser)
    if !ok {
        return nil, fail.InvalidParameterError("p['stdout']", "is missing or is not of type io.ReadCloser")
    }
    if stdoutPipe == nil {
        return nil, fail.InvalidParameterError("p['stdout']", "cannot be nil")
    }
    stderrPipe, ok = params["stderr"].(io.ReadCloser)
    if !ok {
        return nil, fail.InvalidParameterError("p['stderr']", "is missing or is not of type io.ReadCloser")
    }
    if stderrPipe == nil {
        return nil, fail.InvalidParameterError("p['stderr']", "cannot be nil")
    }
    if collectOutputs, ok = params["collect_outputs"].(bool); !ok {
        return nil, fail.InvalidParameterError("p[collect_outputs]", "is missing or is not of type bool")
    }

    var (
        stdoutBridge, stderrBridge cli.PipeBridge
        pipeBridgeCtrl             *cli.PipeBridgeController
        msgOut, msgErr             []byte
        err                        error
    )

    result := data.Map{
        "retcode": -1,
        "stdout":  "",
        "stderr":  "",
    }

    if !collectOutputs {
        stdoutBridge, err = cli.NewStdoutBridge(stdoutPipe)
        if err != nil {
            return result, fail.NewError(err.Error())
        }
        stderrBridge, err = cli.NewStderrBridge(stderrPipe)
        if err != nil {
            return result, fail.NewError(err.Error())
        }
        pipeBridgeCtrl, err = cli.NewPipeBridgeController(stdoutBridge, stderrBridge)
        if err != nil {
            return result, fail.NewError(err.Error())
        }
    }

    // Starts pipebridge if needed
    if !collectOutputs {
        err := pipeBridgeCtrl.Start(task)
        if err != nil {
            return result, err
        }
    }

    // Launch the command and wait for its execution
    if err := sc.Start(); err != nil {
        return result, err
    }

    if collectOutputs {
        msgOut, err = ioutil.ReadAll(stdoutPipe)
        if err != nil {
            return result, fail.ToError(err)
        }

        msgErr, err = ioutil.ReadAll(stderrPipe)
        if err != nil {
            return result, fail.ToError(err)
        }
    }

    var pbcErr error
    err = sc.Wait()
    _ = stdoutPipe.Close()
    _ = stderrPipe.Close()

    if err == nil {
        result["retcode"] = 0
        if collectOutputs {
            result["stdout"] = string(msgOut)
            result["stderr"] = string(msgErr)
        } else {
            pbcErr = pipeBridgeCtrl.Wait()
            if pbcErr != nil {
                logrus.Error(pbcErr.Error())
            }
        }
    } else {
        xerr := fail.ExecutionError(err)
        // If error doesn't contain ouputs and return code of the process, stop the pipe bridges and return error
        var (
            note   data.Annotation
            stderr string
            ok     bool
        )
        if note, ok = xerr.Annotation("retcode"); !ok || note.(int) == -1 {
            if !collectOutputs {
                derr := pipeBridgeCtrl.Stop()
                if derr != nil {
                    _ = xerr.AddConsequence(derr)
                }
            }
            return result, xerr
        }
        result["retcode"] = note.(int)

        // Make sure all outputs have been processed
        if !collectOutputs {
            pbcErr = pipeBridgeCtrl.Wait()
            if pbcErr != nil {
                logrus.Error(pbcErr.Error())
            }

            if note, ok = xerr.Annotation("stderr"); ok {
                result["stderr"] = note.(string)
            }
        } else {
            result["stdout"] = string(msgOut)
            result["stderr"] = fmt.Sprint(string(msgErr), stderr)
        }
    }

    return result, nil
}

func (sc *SSHCommand) cleanup() fail.Error {
    err1 := sc.closeTunneling()
    err2 := utils.LazyRemove(sc.keyFile.Name())
    if err1 != nil {
        logrus.Errorf("closeTunneling() failed: %s\n", reflect.TypeOf(err1).String())
        return fail.Wrap(err1, "unable to close SSH tunnels")
    }
    if err2 != nil {
        return fail.Wrap(err2, "unable to close SSH tunnels")
    }
    return nil
}

func recCreateTunnels(ssh *SSHConfig, tunnels *[]*SSHTunnel) (*SSHTunnel, fail.Error) {
    if ssh != nil {
        tunnel, err := recCreateTunnels(ssh.GatewayConfig, tunnels)
        if err != nil {
            return nil, err
        }
        cfg := ssh
        if tunnel != nil {
            gateway := *ssh.GatewayConfig
            gateway.Port = tunnel.port
            gateway.Host = "127.0.0.1"
            cfg.GatewayConfig = &gateway
        }
        if cfg.GatewayConfig != nil {
            tunnel, err = buildTunnel(cfg)
            if err != nil {
                return nil, err
            }
            *tunnels = append(*tunnels, tunnel)
            return tunnel, err
        }
    }
    return nil, nil
}

// CreateTunneling ...
func (ssh *SSHConfig) CreateTunneling() ([]*SSHTunnel, *SSHConfig, fail.Error) {
    var tunnels []*SSHTunnel
    tunnel, err := recCreateTunnels(ssh, &tunnels)
    if err != nil {
        return nil, nil, fail.Wrap(err, "unable to create SSH Tunnels")
    }
    sshConfig := *ssh
    if tunnel == nil {
        return nil, &sshConfig, nil
    }

    if ssh.GatewayConfig != nil {
        sshConfig.Port = tunnel.port
        sshConfig.Host = "127.0.0.1"
    }
    return tunnels, &sshConfig, nil
}

func createSSHCmd(sshConfig *SSHConfig, cmdString, username, shell string, withTty, withSudo bool) (string, *os.File, fail.Error) {
    f, err := CreateTempFileFromString(sshConfig.PrivateKey, 0400)
    if err != nil {
        return "", nil, fail.Wrap(err, "unable to create temporary key file")
    }

    options := sshOptions + " -oLogLevel=error"

    sshCmdString := fmt.Sprintf("ssh -i %s %s -p %d %s@%s", f.Name(), options, sshConfig.Port, sshConfig.User, sshConfig.Host)

    if shell == "" {
        shell = "bash"
    }
    cmd := ""
    if username != "" {
        cmd = "sudo -u " + username + " -i "
    }

    if withTty {
        // tty option is required for some command like ls
        sshCmdString += " -t"
    }

    if withSudo {
        if cmd == "" {
            // tty option is required for some command like ls
            cmd = "sudo"
        }
    }

    if cmd != "" {
        sshCmdString += " " + cmd + " " + shell
    }

    if cmdString != "" {
        sshCmdString += fmt.Sprintf(" <<'ENDSSH'\n%s\nENDSSH", cmdString)
    }
    return sshCmdString, f, nil

}

// Command returns the cmd struct to execute cmdString remotely
func (ssh *SSHConfig) Command(task concurrency.Task, cmdString string) (*SSHCommand, fail.Error) {
    return ssh.command(task, cmdString, false, false)
}

// SudoCommand returns the cmd struct to execute cmdString remotely. Command is executed with sudo
func (ssh *SSHConfig) SudoCommand(task concurrency.Task, cmdString string) (*SSHCommand, fail.Error) {
    return ssh.command(task, cmdString, false, true)
}

func (ssh *SSHConfig) command(task concurrency.Task, cmdString string, withTty, withSudo bool) (*SSHCommand, fail.Error) {
    if ssh == nil {
        return nil, fail.InvalidInstanceError()
    }
    if task.IsNull() {
        return nil, fail.InvalidParameterError("task", "cannot be nil")
    }
    ctx, err := task.GetContext()
    if err != nil {
        return nil, err
    }

    tunnels, sshConfig, err := ssh.CreateTunneling()
    if err != nil {
        return nil, fail.Wrap(err, "unable to create command")
    }
    sshCmdString, keyFile, err := createSSHCmd(sshConfig, cmdString, "", "", withTty, withSudo)
    if err != nil {
        return nil, fail.Wrap(err, "unable to create command")
    }

    cmd := exec.CommandContext(ctx, "bash", "-c", sshCmdString)
    sshCommand := SSHCommand{
        cmd:     cmd,
        tunnels: tunnels,
        keyFile: keyFile,
    }
    return &sshCommand, nil
}

// WaitServerReady waits until the SSH server is ready
// the 'timeout' parameter is in minutes
func (ssh *SSHConfig) WaitServerReady(task concurrency.Task, phase string, timeout time.Duration) (out string, xerr fail.Error) {
    if ssh == nil {
        return "", fail.InvalidInstanceError()
    }
    if task.IsNull() {
        return "", fail.InvalidParameterError("task", "cannot be nil")
    }
    if phase == "" {
        return "", fail.InvalidParameterError("phase", "cannot be empty string")
    }
    if ssh.Host == "" {
        return "", fail.InvalidInstanceContentError("ssh.Host", "cannot be empty string")
    }

    defer debug.NewTracer(task, tracing.ShouldTrace("ssh"), "('%s',%s)", phase, temporal.FormatDuration(timeout)).Entering().Exiting()
    defer fail.OnExitTraceError(
        fmt.Sprintf("timeout waiting remote SSH phase '%s' of host '%s' for %s", phase, ssh.Host, temporal.FormatDuration(timeout)),
        &xerr,
    )

    originalPhase := phase
    if phase == "ready" {
        phase = "final"
    }

    var (
        retcode        int
        stdout, stderr string
    )

    begins := time.Now()
    retryErr := retry.WhileUnsuccessfulDelay5Seconds(
        func() error {
            taskStatus, _ := task.GetStatus()
            if taskStatus == concurrency.ABORTED {
                return retry.StopRetryError(nil, "operation aborted by user")
            }

            cmd, innerErr := ssh.Command(task, fmt.Sprintf("sudo cat /opt/safescale/var/state/user_data.%s.done", phase))
            if innerErr != nil {
                return innerErr
            }

            retcode, stdout, stderr, innerErr = cmd.RunWithTimeout(task, outputs.COLLECT, timeout)
            if innerErr != nil {
                return innerErr
            }
            if retcode != 0 {
                if retcode == 255 {
                    return fail.NewError("remote SSH not ready: error code: 255; Output [%s]; Error [%s]", stdout, stderr)
                }
                return fail.NewError("remote SSH NOT ready: error code: %d; Output [%s]; Error [%s]", retcode, stdout, stderr)
            }
            return nil
        },
        timeout,
    )
    if retryErr != nil {
        return stdout, retryErr
    }
    logrus.Debugf("host [%s] phase [%s] check successful in [%s]: host stdout is [%s]", ssh.Host, originalPhase, temporal.FormatDuration(time.Since(begins)), stdout)
    return stdout, nil
}

// Copy copies a file/directory from/to local to/from remote
func (ssh *SSHConfig) Copy(task concurrency.Task, remotePath, localPath string, isUpload bool) (errc int, stdout string, stderr string, err fail.Error) {
    return ssh.copy(task, remotePath, localPath, isUpload, 0)
}

// CopyWithTimeout copies a file/directory from/to local to/from remote, and fails after 'timeout'
func (ssh *SSHConfig) CopyWithTimeout(
    task concurrency.Task, remotePath, localPath string, isUpload bool, timeout time.Duration,
) (errc int, stdout string, stderr string, err fail.Error) {

    return ssh.copy(task, remotePath, localPath, isUpload, timeout)
}

// copy copies a file/directory from/to local to/from remote, and fails after 'timeout' (if timeout > 0)
func (ssh *SSHConfig) copy(
    task concurrency.Task,
    remotePath, localPath string,
    isUpload bool,
    timeout time.Duration,
) (errc int, stdout string, stderr string, xerr fail.Error) {

    tunnels, sshConfig, xerr := ssh.CreateTunneling()
    if xerr != nil {
        return 0, "", "", fail.Wrap(xerr, "unable to create tunnels")
    }

    identityfile, xerr := CreateTempFileFromString(sshConfig.PrivateKey, 0400)
    if xerr != nil {
        return 0, "", "", fail.Wrap(xerr, "unable to create temporary key file")
    }

    cmdTemplate, err := template.New("Command").Parse(`scp -i {{.IdentityFile}} -P {{.Port}} {{.Options}} {{if .IsUpload}}"{{.LocalPath}}" {{.User}}@{{.Host}}:"{{.RemotePath}}"{{else}}{{.User}}@{{.Host}}:"{{.RemotePath}}" "{{.LocalPath}}"{{end}}`)
    if err != nil {
        return 0, "", "", fail.Wrap(err, "error parsing command template")
    }

    options := sshOptions + " -oLogLevel=error"
    var copyCommand bytes.Buffer
    if err := cmdTemplate.Execute(&copyCommand, struct {
        IdentityFile string
        Port         int
        Options      string
        User         string
        Host         string
        RemotePath   string
        LocalPath    string
        IsUpload     bool
    }{
        IdentityFile: identityfile.Name(),
        Port:         sshConfig.Port,
        Options:      options,
        User:         sshConfig.User,
        Host:         sshConfig.Host,
        RemotePath:   remotePath,
        LocalPath:    localPath,
        IsUpload:     isUpload,
    }); err != nil {
        return 0, "", "", fail.Wrap(err, "error executing template")
    }

    sshCmdString := copyCommand.String()
    cmd := exec.Command("bash", "-c", sshCmdString)
    sshCommand := SSHCommand{
        cmd:     cmd,
        tunnels: tunnels,
        keyFile: identityfile,
    }

    return sshCommand.RunWithTimeout(task, outputs.COLLECT, timeout)
}

// Enter Enter to interactive shell
func (ssh *SSHConfig) Enter(username, shell string) (xerr fail.Error) {
    tunnels, sshConfig, xerr := ssh.CreateTunneling()
    if xerr != nil {
        for _, t := range tunnels {
            nerr := t.Close()
            if nerr != nil {
                logrus.Warnf("Error closing ssh tunnel: %v", nerr)
            }
        }
        return fail.Wrap(xerr, "unable to create command")
    }

    sshCmdString, keyFile, xerr := createSSHCmd(sshConfig, "", username, shell, true, false)
    if !xerr.IsNull() {
        for _, t := range tunnels {
            nerr := t.Close()
            if nerr != nil {
                logrus.Warnf("Error closing ssh tunnel: %v", nerr)
            }
        }
        if keyFile != nil {
            nerr := utils.LazyRemove(keyFile.Name())
            if nerr != nil {
                logrus.Warnf("Error removing file %v", nerr)
            }
        }
        return fail.Wrap(xerr, "unable to create command")
    }

    bash, err := exec.LookPath("bash")
    if err != nil {
        for _, t := range tunnels {
            nerr := t.Close()
            if nerr != nil {
                logrus.Warnf("Error closing ssh tunnel: %v", nerr)
            }
        }
        if keyFile != nil {
            nerr := utils.LazyRemove(keyFile.Name())
            if nerr != nil {
                logrus.Warnf("Error removing file %v", nerr)
            }
        }
        return fail.Wrap(err, "unable to create command")
    }

    proc := exec.Command(bash, "-c", sshCmdString)
    proc.Stdin = os.Stdin
    proc.Stdout = os.Stdout
    proc.Stderr = os.Stderr
    err = proc.Run()
    nerr := utils.LazyRemove(keyFile.Name())
    if nerr != nil {
        logrus.Warnf("Error removing file %v", nerr)
    }
    if err != nil {
        return fail.ExecutionError(err)
    }
    return nil
}

// // CommandContext is like Command but includes a context.
// //
// // The provided context is used to kill the process (by calling
// // os.Process.Kill) if the context becomes done before the command
// // completes on its own.
// func (ssh *SSHConfig) CommandContext(ctx context.Context, cmdString string) (*SSHCommand, error) {
// 	tunnels, sshConfig, err := ssh.CreateTunneling()
// 	if err != nil {
// 		return nil, fmt.Errorf("unable to create command : %s", err.Error()
// 	}
// 	sshCmdString, keyFile, err := createSSHCmd(sshConfig, cmdString, false)
// 	if err != nil {
// 		return nil, fmt.Errorf("unable to create command : %s", err.Error()
// 	}

// 	cmd := exec.CommandContext(ctx, "bash", "-c", sshCmdString)
// 	sshCommand := SSHCommand{
// 		cmd:     cmd,
// 		tunnels: tunnels,
// 		keyFile: keyFile,
// 	}
// 	return &sshCommand, nil
// }

// CreateKeyPair creates a key pair
func CreateKeyPair() (publicKeyBytes []byte, privateKeyBytes []byte, xerr fail.Error) {
    privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
    publicKey := privateKey.PublicKey
    pub, err := ssh.NewPublicKey(&publicKey)
    if err != nil {
        return nil, nil, fail.ToError(err)
    }

    publicKeyBytes = ssh.MarshalAuthorizedKey(pub)

    priBytes := x509.MarshalPKCS1PrivateKey(privateKey)
    privateKeyBytes = pem.EncodeToMemory(
        &pem.Block{
            Type:  "RSA PRIVATE KEY",
            Bytes: priBytes,
        },
    )
    return publicKeyBytes, privateKeyBytes, nil
}
