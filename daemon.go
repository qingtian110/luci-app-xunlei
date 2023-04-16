package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	SYNOPKG_DSM_VERSION_MAJOR = "7"
	SYNOPKG_DSM_VERSION_MINOR = "1"
	SYNOPKG_DSM_VERSION_BUILD = "1"
	SYNOPKG_PKGNAME           = "pan-xunlei-com"
	SYNOPKG_PKGBASE           = "/var/packages/" + SYNOPKG_PKGNAME
	SYNOPKG_PKGDEST           = SYNOPKG_PKGBASE + "/target"
	SYNOPKG_VAR               = SYNOPKG_PKGDEST + "/var/"
	LAUNCHER_EXE              = SYNOPKG_PKGDEST + "/xunlei-pan-cli-launcher"
	PID_FILE                  = SYNOPKG_VAR + SYNOPKG_PKGNAME + ".pid"
	ENV_FILE                  = SYNOPKG_VAR + SYNOPKG_PKGNAME + ".env"
	LOG_FILE                  = SYNOPKG_VAR + SYNOPKG_PKGNAME + ".log"
	LAUNCH_PID_FILE           = SYNOPKG_VAR + SYNOPKG_PKGNAME + "-launcher.pid"
	LAUNCH_LOG_FILE           = SYNOPKG_VAR + SYNOPKG_PKGNAME + "-launcher.log"
	LAUNCHER_SOCK             = "unix://" + SYNOPKG_VAR + SYNOPKG_PKGNAME + "-launcher.sock"
	SOCK_FILE                 = "unix://" + SYNOPKG_VAR + SYNOPKG_PKGNAME + ".sock"
)

type XunleiDaemon struct {
	Port        int    `json:"port"`
	Internal    bool   `json:"internal"`
	DownloadDIR string `json:"download"`
	closers     []func(ctx context.Context) error
	CONFIG_PATH string `alias:"config" env:"CONFIG_PATH"`
}

func (d *XunleiDaemon) Run(ctx context.Context, args []string) error {
	defer d.Start().Stop()
	<-ctx.Done()
	return nil
}

func (d *XunleiDaemon) Usage() string {
	return "-- Start xunLei main program."
}

func (d *XunleiDaemon) Start() *XunleiDaemon {
	log := Standard("Starting")
	if err := d.loadConfig(); err != nil {
		log.Fatalf("Error loading configuration file: %v", err)
	}

	go func() {
		if err := d.startEngine(); err != nil {
			log.Warnf("%v", err)
		}
	}()

	go func() {
		if err := d.startUI(); err != nil {
			log.Warnf("%v", err)
		}
	}()

	return d
}

func (d *XunleiDaemon) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	for _, closer := range d.closers {
		_ = closer(ctx)
	}
}

func (d *XunleiDaemon) loadConfig() error {
	if d.CONFIG_PATH == "" {
		d.CONFIG_PATH = "/etc/xunlei"
	}
	data, err := os.ReadFile(filepath.Join(d.CONFIG_PATH, "config.json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, d)
}

func (d *XunleiDaemon) address() string {
	if d.Internal {
		return fmt.Sprintf("127.0.0.1:%d", d.Port)
	}
	return fmt.Sprintf(":%d", d.Port)
}

func (d *XunleiDaemon) getEnv() (environs []string) {
	if d.CONFIG_PATH == "" {
		d.CONFIG_PATH = "/etc/xunlei"
	}
	environs = os.Environ()
	environs = append(environs, `DriveListen=`+SOCK_FILE)
	environs = append(environs, fmt.Sprintf(`OS_VERSION="dsm %s.%s-%s"`, SYNOPKG_DSM_VERSION_MAJOR, SYNOPKG_DSM_VERSION_MINOR, SYNOPKG_DSM_VERSION_BUILD))
	environs = append(environs, `HOME=`+d.CONFIG_PATH)
	environs = append(environs, `ConfigPath=`+d.CONFIG_PATH)
	environs = append(environs, `DownloadPATH=`+d.DownloadDIR)
	environs = append(environs, "SYNOPKG_DSM_VERSION_MAJOR="+SYNOPKG_DSM_VERSION_MAJOR)
	environs = append(environs, "SYNOPKG_DSM_VERSION_MINOR="+SYNOPKG_DSM_VERSION_MINOR)
	environs = append(environs, "SYNOPKG_DSM_VERSION_BUILD="+SYNOPKG_DSM_VERSION_BUILD)
	environs = append(environs, "SYNOPKG_PKGDEST="+SYNOPKG_PKGDEST)
	environs = append(environs, "SYNOPKG_PKGNAME="+SYNOPKG_PKGNAME)
	environs = append(environs, "SVC_CWD="+SYNOPKG_PKGDEST)
	environs = append(environs, "PID_FILE="+PID_FILE)
	environs = append(environs, "ENV_FILE="+ENV_FILE)
	environs = append(environs, "LOG_FILE="+LOG_FILE)
	environs = append(environs, "LAUNCH_LOG_FILE="+LAUNCH_LOG_FILE)
	environs = append(environs, "LAUNCH_PID_FILE="+LAUNCH_PID_FILE)
	environs = append(environs, "GIN_MODE=release")
	return
}

// Start panel
func (d *XunleiDaemon) startUI() error {
	log := Standard("UI")
	mux := chi.NewMux()
	mux.Use(middleware.Recoverer)

	home := "/webman/3rdparty/" + SYNOPKG_PKGNAME + "/index.cgi"
	// Jump
	jump := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { http.Redirect(rw, r, home+"/", 307) })
	mux.Handle("/", jump)
	mux.Handle("/webman/", jump)
	mux.Handle("/webman/3rdparty/"+SYNOPKG_PKGNAME, jump)

	// Xunlei panel CGI
	mux.Handle(home+"/*",
		&cgi.Handler{
			Path: filepath.Join(SYNOPKG_PKGDEST, "xunlei-pan-cli-web"),
			Root: SYNOPKG_PKGDEST,
			Dir:  SYNOPKG_PKGDEST,
			Env:  d.getEnv(),
		},
	)

	// Mock Synology login
	mux.Handle("/webman/login.cgi",
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json; charset=utf-8")
			rw.WriteHeader(200)
			rw.Write([]byte(`{"SynoToken":""}`))
		}),
	)

	listenAddress := d.address()
	log.Infof("Starting")
	log.Infof("Listening port: %s", listenAddress)

	server := &http.Server{Addr: listenAddress, Handler: mux, BaseContext: func(l net.Listener) context.Context {
		log.Infof("Started: %s", l.Addr())
		return context.Background()
	}}

	d.closers = append(d.closers, server.Shutdown)
	if err := server.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			log.Infof("Quit")
		} else {
			log.Warnf("Quit: %v", err)
			return err
		}
	}
	return nil
}

// Start service
func (d *XunleiDaemon) startEngine() error {
	log := Standard("Xunlei Daemon")
	os.MkdirAll(SYNOPKG_VAR, 0755)
	daemon := exec.Command(LAUNCHER_EXE, "-launcher_listen="+LAUNCHER_SOCK, "-pid="+PID_FILE, "-logfile="+LAUNCH_LOG_FILE)
	daemon.Dir = SYNOPKG_PKGDEST
	daemon.Env = d.getEnv()
	daemon.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	daemon.Stderr = os.Stderr
	daemon.Stdout = os.Stdout
	daemon.Stdin = os.Stdin

	log.Infof("Starting")
	log.Infof("Command: %s", daemon.String())
	if err := daemon.Start(); err != nil {
		return err
	}

	log.Infof("PID: %d", daemon.Process.Pid)

	d.closers = append(d.closers, func(ctx context.Context) error {
		return syscall.Kill(-daemon.Process.Pid, syscall.SIGINT)
	})

	err := daemon.Wait()
	if daemon.ProcessState != nil {
		log.Infof("Quit: %s", daemon.ProcessState.String())
	} else {
		log.Infof("Quit")
	}
	return err
}
