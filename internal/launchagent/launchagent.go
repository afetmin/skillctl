package launchagent

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"skillctl/internal/fileutil"
)

const Label = "dev.skillctl.watch"

type Definition struct {
	Executable      string
	ConfigPath      string
	StatePath       string
	Interval        string
	LogPath         string
	WorkingDir      string
	EnvironmentPath string
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

func Install(definition Definition) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("LaunchAgent installation is only available on macOS")
	}
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(definition.LogPath), 0o755); err != nil {
		return "", err
	}
	data, err := Render(definition)
	if err != nil {
		return "", err
	}
	if err := fileutil.WriteAtomic(path, data, 0o644); err != nil {
		return "", err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+Label).Run()
	output, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("launchctl bootstrap: %w: %s", err, bytes.TrimSpace(output))
	}
	return path, nil
}

func Uninstall() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("LaunchAgent installation is only available on macOS")
	}
	path, err := Path()
	if err != nil {
		return "", err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	output, bootErr := exec.Command("launchctl", "bootout", domain+"/"+Label).CombinedOutput()
	if bootErr != nil && !bytes.Contains(output, []byte("Could not find specified service")) {
		return "", fmt.Errorf("launchctl bootout: %w: %s", bootErr, bytes.TrimSpace(output))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func Status() (bool, string, error) {
	path, err := Path()
	if err != nil {
		return false, "", err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, path, nil
	}
	return err == nil, path, err
}

func Render(definition Definition) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString(xml.Header)
	buffer.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	encoder := xml.NewEncoder(&buffer)
	encoder.Indent("", "  ")
	plist := xml.StartElement{Name: xml.Name{Local: "plist"}, Attr: []xml.Attr{{Name: xml.Name{Local: "version"}, Value: "1.0"}}}
	if err := encoder.EncodeToken(plist); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "dict"}}); err != nil {
		return nil, err
	}
	writeString := func(key, value string) error {
		if err := encoder.EncodeElement(key, xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
			return err
		}
		return encoder.EncodeElement(value, xml.StartElement{Name: xml.Name{Local: "string"}})
	}
	if err := writeString("Label", Label); err != nil {
		return nil, err
	}
	if err := encoder.EncodeElement("ProgramArguments", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "array"}}); err != nil {
		return nil, err
	}
	arguments := []string{
		definition.Executable,
		"--config", definition.ConfigPath,
		"--state", definition.StatePath,
		"--cwd", definition.WorkingDir,
		"watch", "--interval", definition.Interval,
	}
	for _, argument := range arguments {
		if err := encoder.EncodeElement(argument, xml.StartElement{Name: xml.Name{Local: "string"}}); err != nil {
			return nil, err
		}
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "array"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeElement("RunAtLoad", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "true"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "true"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeElement("KeepAlive", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "true"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "true"}}); err != nil {
		return nil, err
	}
	if err := writeString("StandardOutPath", definition.LogPath); err != nil {
		return nil, err
	}
	if err := writeString("StandardErrorPath", definition.LogPath); err != nil {
		return nil, err
	}
	if err := writeString("WorkingDirectory", definition.WorkingDir); err != nil {
		return nil, err
	}
	if err := encoder.EncodeElement("EnvironmentVariables", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "dict"}}); err != nil {
		return nil, err
	}
	if err := writeString("PATH", definition.EnvironmentPath); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "dict"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeElement("ThrottleInterval", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeElement("30", xml.StartElement{Name: xml.Name{Local: "integer"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "dict"}}); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(plist.End()); err != nil {
		return nil, err
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	buffer.WriteByte('\n')
	return buffer.Bytes(), nil
}
