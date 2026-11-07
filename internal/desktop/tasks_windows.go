package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

type TaskState string

const (
	TaskDisabled TaskState = "Disabled"
	TaskEnabled  TaskState = "Enabled"
	TaskConflict TaskState = "Conflict"
)

// TaskStore makes scheduler operations replaceable in tests. Test stores never
// fall back to the real scheduler, even when an operation fails.
type TaskStore interface {
	Read(name string) ([]byte, bool, error)
	Create(name string, definition []byte) error
	Enable(name string) error
	Delete(name string) error
}

type Scheduler struct {
	// Tests replace the command boundary so they cannot register real tasks.
	run func(args ...string) ([]byte, error)
}

func (s Scheduler) command(args ...string) ([]byte, error) {
	if s.run != nil {
		return s.run(args...)
	}
	return schedulerCommand(args...)
}

func schedulerCommand(args ...string) ([]byte, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	command := exec.Command(filepath.Join(system, "schtasks.exe"), args...)
	Hidden(command)
	return command.CombinedOutput()
}

func schedulerError(message string, output []byte, err error) error {
	detail := strings.TrimSpace(string(decodeTaskXML(output)))
	if detail == "" {
		return fmt.Errorf("%s: %w", message, err)
	}
	return fmt.Errorf("%s: %w\n%s", message, err, detail)
}

func (s Scheduler) Read(name string) ([]byte, bool, error) {
	data, queryErr := s.command("/Query", "/TN", name, "/XML")
	if queryErr == nil {
		return decodeTaskXML(data), true, nil
	}
	// A failed named query can mean access denied, not just a missing task.
	// Check the task list before reporting Disabled; do not parse localized errors.
	list, err := s.command("/Query", "/FO", "CSV", "/NH")
	if err != nil {
		return nil, false, schedulerError("无法查询计划任务", list, err)
	}
	reader := csv.NewReader(bytes.NewReader(list))
	reader.FieldsPerRecord = -1
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("无法读取计划任务列表: %w", err)
		}
		if len(row) > 0 && strings.EqualFold(strings.TrimPrefix(row[0], `\`), strings.TrimPrefix(name, `\`)) {
			return nil, false, schedulerError("计划任务存在但无法读取", data, queryErr)
		}
	}
	return nil, false, nil
}

func (s Scheduler) Create(name string, definition []byte) error {
	file, err := os.CreateTemp("", "octopus-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(encodeTaskXML(definition)); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	// Never force-overwrite a task created between the ownership check and Create.
	if output, err := s.command("/Create", "/TN", name, "/XML", file.Name()); err != nil {
		return schedulerError("无法创建登录计划任务", output, err)
	}
	return nil
}
func (s Scheduler) Enable(name string) error {
	if output, err := s.command("/Change", "/TN", name, "/ENABLE"); err != nil {
		return schedulerError("无法启用登录计划任务", output, err)
	}
	return nil
}
func (s Scheduler) Delete(name string) error {
	if output, err := s.command("/Delete", "/TN", name, "/F"); err != nil {
		return schedulerError("无法移除登录计划任务", output, err)
	}
	return nil
}

func encodeTaskXML(definition []byte) []byte {
	// schtasks imports XML through a UTF-16 string. The file's declaration,
	// byte encoding and BOM must agree, including paths outside the BMP.
	const header = "<?xml version=\"1.0\" encoding=\"UTF-16\"?>\n"
	document := header + strings.TrimPrefix(string(definition), xml.Header)
	units := utf16.Encode([]rune(document))
	data := make([]byte, 2+2*len(units))
	data[0], data[1] = 0xff, 0xfe
	for i, unit := range units {
		data[2+2*i], data[3+2*i] = byte(unit), byte(unit>>8)
	}
	return data
}

func decodeTaskXML(data []byte) []byte {
	if len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe || data[1] == 0) {
		if data[0] == 0xff {
			data = data[2:]
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
		}
		data = []byte(string(utf16.Decode(units)))
	}
	return bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
}

type taskTrigger struct {
	XMLName xml.Name
	UserID  string `xml:"UserId"`
	Enabled *bool  `xml:"Enabled"`
}
type taskAction struct {
	XMLName   xml.Name
	Command   string `xml:"Command"`
	Arguments string `xml:"Arguments"`
	Directory string `xml:"WorkingDirectory"`
}
type taskPrincipal struct {
	UserID string `xml:"UserId"`
	Logon  string `xml:"LogonType"`
	Level  string `xml:"RunLevel"` // Omitted means the schema default, LeastPrivilege.
}
type taskDocument struct {
	XMLName  xml.Name `xml:"Task"`
	Triggers struct {
		Items []taskTrigger `xml:",any"`
	} `xml:"Triggers"`
	Actions struct {
		Items []taskAction `xml:",any"`
	} `xml:"Actions"`
	Principals []taskPrincipal `xml:"Principals>Principal"`
	Enabled    *bool           `xml:"Settings>Enabled"`
}

func taskEnabled(value *bool) bool {
	// Task Scheduler omits Enabled when it has the schema default of true.
	return value == nil || *value
}

func taskUserMatches(value, userID string) bool {
	if value == "" {
		return false // An omitted logon user would trigger for every user.
	}
	sid, err := windows.StringToSid(value)
	if err != nil {
		// Windows can export DOMAIN\user instead of the submitted SID. Resolve
		// the identity rather than accepting an unverified account-name match.
		sid, _, _, err = windows.LookupSID("", value)
	}
	return err == nil && sid.String() == userID
}

type Autostart struct {
	directory, userID string
	store             TaskStore
}

func NewAutostart(directory string, store TaskStore) (*Autostart, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, fmt.Errorf("读取当前 Windows 用户: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("读取当前 Windows 用户标识: %w", err)
	}
	return &Autostart{directory: directory, userID: user.User.Sid.String(), store: store}, nil
}
func (a *Autostart) Name() string {
	userHash := sha256.Sum256([]byte(a.userID))
	return "Octopus Roam-" + Identity(a.directory)[:20] + "-" + hex.EncodeToString(userHash[:6])
}
func (a *Autostart) arguments() string {
	return "desktop boot --runtime " + syscall.EscapeArg(a.directory)
}
func xmlText(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}
func (a *Autostart) Definition() []byte {
	return []byte(xml.Header + `<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo><Description>Start this Octopus Roam deployment after Windows sign-in.</Description></RegistrationInfo>
<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(a.userID) + `</UserId></LogonTrigger></Triggers>
<Principals><Principal id="CurrentUser"><UserId>` + xmlText(a.userID) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>false</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings>
<Actions Context="CurrentUser"><Exec><Command>` + xmlText(filepath.Join(a.directory, "octopus-roam.exe")) + `</Command><Arguments>` + xmlText(a.arguments()) + `</Arguments><WorkingDirectory>` + xmlText(a.directory) + `</WorkingDirectory></Exec></Actions>
</Task>`)
}
func (a *Autostart) inspect() (TaskState, bool, error) {
	data, exists, err := a.store.Read(a.Name())
	if err != nil || !exists {
		return TaskDisabled, exists, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(decodeTaskXML(data)))
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "utf-16") || strings.EqualFold(label, "utf-8") {
			return input, nil
		}
		return nil, fmt.Errorf("unsupported task encoding")
	}
	var document taskDocument
	if decoder.Decode(&document) != nil {
		return TaskConflict, true, nil
	}
	if len(document.Actions.Items) != 1 || len(document.Triggers.Items) != 1 || len(document.Principals) != 1 {
		return TaskConflict, true, nil
	}
	action, trigger, principal := document.Actions.Items[0], document.Triggers.Items[0], document.Principals[0]
	if action.XMLName.Local != "Exec" || trigger.XMLName.Local != "LogonTrigger" ||
		!strings.EqualFold(action.Command, filepath.Join(a.directory, "octopus-roam.exe")) ||
		action.Arguments != a.arguments() || !strings.EqualFold(action.Directory, a.directory) ||
		!taskEnabled(trigger.Enabled) || !taskUserMatches(trigger.UserID, a.userID) || !taskUserMatches(principal.UserID, a.userID) ||
		principal.Logon != "InteractiveToken" || (principal.Level != "" && principal.Level != "LeastPrivilege") {
		return TaskConflict, true, nil
	}
	if !taskEnabled(document.Enabled) {
		return TaskDisabled, true, nil
	}
	return TaskEnabled, true, nil
}
func (a *Autostart) State() (TaskState, error) { state, _, err := a.inspect(); return state, err }
func (a *Autostart) Enable() error {
	state, exists, err := a.inspect()
	if err != nil {
		return err
	}
	if state == TaskConflict {
		return fmt.Errorf("此部署的计划任务已被修改，请在任务计划程序中检查其目标和参数")
	}
	if state == TaskEnabled {
		return nil
	}
	if exists {
		return a.store.Enable(a.Name())
	}
	return a.store.Create(a.Name(), a.Definition())
}
func (a *Autostart) Disable() error {
	state, exists, err := a.inspect()
	if err != nil {
		return err
	}
	if state == TaskConflict {
		return fmt.Errorf("此部署的计划任务已被修改，无法确认归属")
	}
	if !exists {
		return nil
	}
	return a.store.Delete(a.Name())
}
