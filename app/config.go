// Copyright 2016 Martin Hebnes Pedersen (LA5NTA). All rights reserved.
// Use of this source code is governed by the MIT-license that can be
// found in the LICENSE file.

package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"strings"

	"github.com/kelseyhightower/envconfig"
	"github.com/la5nta/pat/cfg"
	"github.com/la5nta/pat/internal/buildinfo"
	"github.com/la5nta/pat/internal/debug"
)

func LoadConfig(cfgPath string, fallback cfg.Config) (config cfg.Config, err error) {
	config, err = ReadConfig(cfgPath)
	switch {
	case os.IsNotExist(err):
		config = fallback
		if err := WriteConfig(config, cfgPath); err != nil {
			return config, err
		}
	case err != nil:
		return config, err
	}

	// Environment variables overrides values from the config file
	if err := envconfig.Process(buildinfo.AppName, &config); err != nil {
		return config, err
	}
	// Environment variables for hamlib rigs (custom syntax not handled by envconfig)
	if err := readRigsFromEnv(&config.HamlibRigs); err != nil {
		return config, err
	}

	// Ensure the alias "telnet" exists
	if config.ConnectAliases == nil {
		config.ConnectAliases = make(map[string]string)
	}
	if _, exists := config.ConnectAliases["telnet"]; !exists {
		config.ConnectAliases["telnet"] = cfg.DefaultConfig.ConnectAliases["telnet"]
	}

	// TODO: Remove after some release cycles (2023-05-21)
	// Rewrite deprecated serial-tnc:// aliases to ax25-serial-tnc://
	var deprecatedAliases []string
	for k, v := range config.ConnectAliases {
		if !strings.HasPrefix(v, MethodSerialTNCDeprecated+"://") {
			continue
		}
		deprecatedAliases = append(deprecatedAliases, k)
		config.ConnectAliases[k] = strings.Replace(v, MethodSerialTNCDeprecated, MethodAX25SerialTNC, 1)
	}
	if len(deprecatedAliases) > 0 {
		log.Printf("Alias(es) %s uses deprecated transport scheme %s://. Please use %s:// instead.", strings.Join(deprecatedAliases, ", "), MethodSerialTNCDeprecated, MethodAX25SerialTNC)
	}

	// Ensure ServiceCodes has a default value
	if len(config.ServiceCodes) == 0 {
		config.ServiceCodes = cfg.DefaultConfig.ServiceCodes
	}

	// Ensure we have a default AX.25 engine
	if config.AX25.Engine == "" {
		config.AX25.Engine = cfg.DefaultAX25Engine()
	}

	// Ensure we have a default AGWPE config
	if config.AGWPE == (cfg.AGWPEConfig{}) {
		config.AGWPE = cfg.DefaultConfig.AGWPE
	}

	// Enforce minimum beacon intervals
	if config.Ardop.BeaconInterval > 0 && config.Ardop.BeaconInterval < 10 {
		config.Ardop.BeaconInterval = 10
	}
	if config.AX25.Beacon.Every > 0 && config.AX25.Beacon.Every < 10 {
		config.AX25.Beacon.Every = 10
	}

	// Ensure we have a default AX.25 Linux config
	if config.AX25Linux == (cfg.AX25LinuxConfig{}) {
		config.AX25Linux = cfg.DefaultConfig.AX25Linux
	}
	// TODO: Remove after some release cycles (2023-04-30)
	if v := config.AX25.AXPort; v != "" && v != config.AX25Linux.Port {
		log.Println("Using deprecated configuration option ax25.port. Please set ax25_linux.port instead.")
		config.AX25Linux.Port = v
	}

	// Ensure Pactor has a default value
	if config.Pactor == (cfg.PactorConfig{}) {
		config.Pactor = cfg.DefaultConfig.Pactor
	}

	// Ensure VARA FM and VARA HF has default values
	if config.VaraHF.IsZero() {
		config.VaraHF = cfg.DefaultConfig.VaraHF
	}
	if config.VaraFM.IsZero() {
		config.VaraFM = cfg.DefaultConfig.VaraFM
	}

	// Ensure GPSd has a default value
	if config.GPSd == (cfg.GPSdConfig{}) {
		config.GPSd = cfg.DefaultConfig.GPSd
	}
	// TODO: Remove after some release cycles (2019-09-29)
	if v := config.GPSdAddrLegacy; v != "" && v != config.GPSd.Addr {
		log.Println("Using deprecated configuration option gpsd_addr. Please set gpsd.addr instead.")
		config.GPSd.Addr = v
	}

	// Ensure SerialTNC has a default hbaud and serialbaud
	if config.SerialTNC.HBaud == 0 {
		config.SerialTNC.HBaud = cfg.DefaultConfig.SerialTNC.HBaud
	}
	if config.SerialTNC.SerialBaud == 0 {
		config.SerialTNC.SerialBaud = cfg.DefaultConfig.SerialTNC.SerialBaud
	}
	// Compatibility for the old baudrate field for serial-tnc
	if v := config.SerialTNC.BaudrateLegacy; v != 0 && v != config.SerialTNC.HBaud {
		// Since we changed the default value from 9600 to 1200, we can't warn about this without causing confusion.
		debug.Printf("Legacy serial_tnc.baudrate config detected (%d). Translating to serial_tnc.hbaud.", v)
		config.SerialTNC.HBaud = v
	}

	// Ensure ARDOP.ConnectRequests has a default value
	if config.Ardop.ConnectRequests == 0 {
		config.Ardop.ConnectRequests = cfg.DefaultConfig.Ardop.ConnectRequests
	}

	return config, nil
}

// readRigsFromEnv reads hamlib rigs config from environment.
// Syntax: PAT_HAMLIB_RIGS_{rig name}_{ATTRIBUTE}
// _{ATTRIBUTE} is optional (defaults to _ADDRESS).
// Examples:
//   - PAT_HAMLIB_RIGS_rig1_NETWORK=tcp
//   - PAT_HAMLIB_RIGS_rig1_ADDRESS=localhost:8080
//   - PAT_HAMLIB_RIGS_rig1_VFO=A
//   - PAT_HAMLIB_RIGS_rig2=localhost:8080
func readRigsFromEnv(rigs *map[string]cfg.HamlibConfig) error {
	prefix := strings.ToUpper(buildinfo.AppName) + "_HAMLIB_RIGS_"
	for _, env := range os.Environ() {
		attribute, value, _ := strings.Cut(env, "=")
		if !strings.HasPrefix(attribute, prefix) {
			continue
		}
		attribute = strings.TrimPrefix(attribute, prefix)
		name, attribute, _ := strings.Cut(attribute, "_")
		if *rigs == nil {
			*rigs = make(map[string]cfg.HamlibConfig)
		}
		rig := (*rigs)[name]
		switch attribute {
		case "ADDRESS", "":
			rig.Address = value
		case "NETWORK":
			rig.Network = value
		case "VFO":
			rig.VFO = value
		default:
			return fmt.Errorf("invalid attribute '%s' for rig '%s'", attribute, name)
		}
		(*rigs)[name] = rig
	}
	return nil
}

func ReadConfig(path string) (config cfg.Config, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	err = json.Unmarshal(data, &config)
	return
}

func WriteConfig(config cfg.Config, filePath string) error {
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// Add trailing new-line
	b = append(b, '\n')

	// Ensure path dir is available
	os.Mkdir(path.Dir(filePath), os.ModePerm|os.ModeDir)

	return os.WriteFile(filePath, b, 0o600)
}
