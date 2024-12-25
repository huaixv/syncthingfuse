package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path"

	"github.com/boltdb/bolt"
	"github.com/calmh/logger"
	"github.com/huaixv/syncthingfuse/lib/config"
	"github.com/huaixv/syncthingfuse/lib/model"
	"github.com/syncthing/syncthing/lib/connections"
	"github.com/syncthing/syncthing/lib/connections/registry"
	"github.com/syncthing/syncthing/lib/discover"
	"github.com/syncthing/syncthing/lib/events"
	stlogger "github.com/syncthing/syncthing/lib/logger"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/thejerf/suture/v4"
)

var (
	Version     = "unknown-dev"
	LongVersion = Version
)

var (
	cfg     *config.Wrapper
	myID    protocol.DeviceID
	confDir string
	stop    = make(chan int)
	cert    tls.Certificate
	lans    []*net.IPNet
	m       *model.Model
)

const (
	bepProtocolName = "bep/1.0"
)

var l = logger.DefaultLogger

// Command line and environment options
var (
	showVersion bool
)

const (
	usage      = "syncthingfuse [options]"
	extraUsage = `
The default configuration directory is:

  %s

`
)

func main() {
	flag.BoolVar(&showVersion, "version", false, "Show version")

	flag.Usage = usageFor(flag.CommandLine, usage, fmt.Sprintf(extraUsage, baseDirs["config"]))
	flag.Parse()

	if showVersion {
		fmt.Println(Version)
		return
	}

	if err := expandLocations(); err != nil {
		l.Fatalln(err)
	}

	// Ensure that our home directory exists.
	ensureDir(baseDirs["config"], 0700)
	ensureDir(baseDirs["cache"], 0700)

	// Ensure that that we have a certificate and key.
	tlsCfg, cert := getTlsConfig()

	// We reinitialize the predictable RNG with our device ID, to get a
	// sequence that is always the same but unique to this syncthing instance.
	predictableRandom.Seed(seedFromBytes(cert.Certificate[0]))

	myID = protocol.NewDeviceID(cert.Certificate[0])
	l.SetPrefix(fmt.Sprintf("[%s] ", myID.String()[:5]))
	l.SetFlags(log.Lshortfile | log.LstdFlags)
	stlogger.DefaultLogger.SetFlags(log.Lshortfile | log.LstdFlags)

	l.Infoln("Started syncthingfuse v.", LongVersion)
	l.Infoln("My ID:", myID)

	cfg := getConfiguration()

	if info, err := os.Stat(cfg.Raw().MountPoint); err == nil {
		if !info.Mode().IsDir() {
			l.Fatalln("Mount point (", cfg.Raw().MountPoint, ") must be a directory, but isn't")
			os.Exit(1)
		}
	} else {
		l.Infoln("Mount point (", cfg.Raw().MountPoint, ") does not exist, creating it")
		err = os.MkdirAll(cfg.Raw().MountPoint, 0700)
		if err != nil {
			l.Warnln("Error creating mount point", cfg.Raw().MountPoint, err)
			l.Warnln("Sometimes, SyncthingFUSE doesn't shut down and unmount cleanly,")
			l.Warnln("If you don't know of any other file systems you have mounted at")
			l.Warnln("the mount point, try running the command below to unmount, then")
			l.Warnln("start SyncthingFUSE again.")
			l.Warnln("    umount", cfg.Raw().MountPoint)
			l.Fatalln("Cannot create missing mount point")
			os.Exit(1)
		}
	}

	evLogger := events.NewLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mainSvc := suture.New("main", suture.Spec{})
	mainSvc.ServeBackground(ctx)

	database := openDatabase(cfg)

	keyGen := protocol.NewKeyGenerator()
	m = model.NewModel(ctx, cfg, database)

	stCfg := cfg.AsStCfg(myID)

	// Start discovery and connection management
	addrLister := &lateAddressLister{}

	connRegistry := registry.New()
	discoveryManager := discover.NewManager(myID, stCfg, cert, evLogger, addrLister, connRegistry)
	connectionsService := connections.NewService(stCfg, myID, m, tlsCfg, discoveryManager, bepProtocolName, tlsDefaultCommonName, evLogger, connRegistry, keyGen)

	addrLister.AddressLister = connectionsService

	mainSvc.Add(discoveryManager)
	mainSvc.Add(connectionsService)

	if cfg.Raw().Options.GlobalAnnounceEnabled {
		for _, srv := range cfg.Raw().Options.GlobalAnnounceServers {
			l.Infoln("Using discovery server", srv)
			_, err := discover.NewGlobal(srv, cert, addrLister, evLogger, connRegistry)
			if err != nil {
				l.Warnln("Global discovery:", err)
			}
		}
	}

	if cfg.Raw().Options.LocalAnnounceEnabled {
		// v4 broadcasts
		_, err := discover.NewLocal(myID, fmt.Sprintf(":%d", cfg.Raw().Options.LocalAnnouncePort), connectionsService, evLogger)
		if err != nil {
			l.Warnln("IPv4 local discovery:", err)
		}
		// v6 multicasts
		_, err1 := discover.NewLocal(myID, cfg.Raw().Options.LocalAnnounceMCAddr, connectionsService, evLogger)
		if err1 != nil {
			l.Warnln("IPv6 local discovery:", err)
		}
	}

	if cfg.Raw().GUI.Enabled {
		api, err := newAPISvc(myID, cfg, m)
		if err != nil {
			l.Fatalln("Cannot start GUI:", err)
		}
		mainSvc.Add(api)
	}

	l.Infoln("Started ...")

	MountFuse(cfg.Raw().MountPoint, m, mainSvc) // TODO handle fight between FUSE and Syncthing Service

	l.Okln("Exiting")

	return
}

func openDatabase(cfg *config.Wrapper) *bolt.DB {
	databasePath := path.Join(cfg.CachePath(), "boltdb")
	database, _ := bolt.Open(databasePath, 0600, nil) // TODO check error
	return database
}

type lateAddressLister struct {
	discover.AddressLister
}
