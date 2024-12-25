angular.module('syncthingfuse.core').controller('SyncthingFuseController', function ($scope, $http) {
    $scope.config = { devices: [] };
    $scope.connections = {};
    $scope.pinnedFileStatus = {};
    $scope.configInSync = true;

    function initController() {
        $scope.refresh()
        setInterval($scope.refresh, 10000);
    }

    $scope.refresh = function() {
        $http.get('/api/system/connections').then(
            function(response) {
                var newConnections = {};
                response.data.forEach(function(connection) {
                    newConnections[connection.DeviceID] = connection
                });
                $scope.connections = newConnections;
            },
            function() { /* TODO handle error */ });

        $http.get('/api/system/pins/status').then(
            function(response) {
                $scope.pinnedFileStatus = response.data;
            },
            function() { /* TODO handle error */ });
    };

    $scope.isDeviceConnected = function(deviceID) {
        if ($scope.connections.hasOwnProperty(deviceID)) {
            return true;
        }
        return false;
    };

    $http.get('/api/system/config').success(function(data) {
        $scope.config = data;

        $scope.config.folders.sort(function(a, b) {
            return a.id.localeCompare(b.id);
        });
        $scope.config.devices.sort(function(a, b) {
            return a.deviceID.localeCompare(b.deviceID);
        });
    });

    $http.get('/api/system/config/insync').then(
        function(response) { $scope.configInSync = response.data; },
        function() { /* TODO handle error */ });

    $scope.findDevice = function (deviceID) {
        var matches = $scope.config.devices.filter(function (n) { return n.deviceID === deviceID; });
        if (matches.length !== 1) {
            return undefined;
        }
        return matches[0];
    };

    $scope.sharesFolder = function (folderCfg) {
        var names = [];
        folderCfg.devices.forEach(function (device) {
            if (device.deviceID != $scope.config.myID) {
                names.push($scope.deviceName($scope.findDevice(device.deviceID)));
            }
        });
        names.sort();
        return names.join(", ");
    };

    $scope.thisDevice = function () {
        for (var i = 0; i < $scope.config.devices.length; i++) {
            var device = $scope.config.devices[i];
            if (device.deviceID === $scope.config.myID) {
                return device;
            }
        }
    };

    $scope.otherDevices = function() {
        var devices = [];

        for (var i=0 ; i<$scope.config.devices.length ; i++) {
            device = $scope.config.devices[i];
            if (device.deviceID !== $scope.config.myID) {
                devices.push(device)
            }
        }

        return devices;
    };

    $scope.deviceName = function (deviceCfg) {
        if (typeof deviceCfg === 'undefined') {
            return "";
        }
        if (deviceCfg.name) {
            return deviceCfg.name;
        }
        return deviceCfg.deviceID.substr(0, 6);
    };

    $scope.addDevice = function() {
        $scope.currentDevice = {
            deviceID: '',
            _addressesStr: 'dynamic',
            compression: 'metadata',
            introducer: false,
            selectedFolders: {}
        };
        $scope.editingExisting = false;
        $scope.deviceEditor.$setPristine();
        $('#editDevice').modal();
    };

    $scope.editDevice = function(device) {
        $scope.currentDevice = angular.copy(device);
        $scope.editingExisting = true;
        $scope.currentDevice._addressesStr = device.addresses.join(', ');

        $scope.currentDevice.selectedFolders = {};
        for (var i=0 ; i<$scope.config.folders.length ; i++) {
            var folder = $scope.config.folders[i];
            for (var j=0 ; j<folder.devices.length ; j++) {
                if (folder.devices[j].deviceID === device.deviceID) {
                    $scope.currentDevice.selectedFolders[folder.id] = true;
                }
            }
        }

        $scope.deviceEditor.$setPristine();
        $('#editDevice').modal();
    };

    $scope.saveDevice = function() {
        $('#editDevice').modal('hide');

        var deviceCfg = $scope.currentDevice;

        if ($scope.editingExisting) {
            // replace existing device
            var i = $scope.config.devices.findIndex(function(el) { return el.deviceID === deviceCfg.deviceID });
            $scope.config.devices[i] = deviceCfg;
        } else {
            // add to devices
            $scope.config.devices.push(deviceCfg);
            $scope.config.devices.sort(function(a, b) {
                return a.deviceID.localeCompare(b.deviceID);
            });
        }

        // edit device
        deviceCfg.addresses = deviceCfg._addressesStr.split(',').map(function (x) {
            return x.trim();
        });

        // manipulate folder configurations
        for (var i=0 ; i<$scope.config.folders.length ; i++) {
            var folder = $scope.config.folders[i];

            var j = folder.devices.findIndex(function(el) { return el.deviceID === deviceCfg.deviceID });

            if (j === -1 && deviceCfg.selectedFolders[folder.id]) {
                // device doesn't exist for folder, but should
                folder.devices.push({deviceID: deviceCfg.deviceID});
            }
            if (j !== -1 && false === deviceCfg.selectedFolders[folder.id]) {
                // device exists for folder, but shouldn't
                folder.devices.splice(j, 1);
            }
        }

        $scope.saveConfig();
    };

    $scope.deleteDevice = function() {
        $('#editDevice').modal('hide');

        var deviceCfg = $scope.currentDevice;

        // remove from shares
        for (var i=0 ; i<$scope.config.folders.length ; i++) {
            var folder = $scope.config.folders[i];
            var j = folder.devices.findIndex(function(el) { return el.deviceID === deviceCfg.deviceID });
            if (j !== -1) {
                // device exists for folder, but shouldn't
                folder.devices.splice(j, 1)
            }
        }

        // remove from devices
        var i = $scope.config.devices.findIndex(function(el) { return el.deviceID === deviceCfg.deviceID });
        $scope.config.devices.splice(i, 1)

        $scope.saveConfig();
    };

    $scope.editSettings = function() {
        $scope.currentDevice = angular.copy($scope.thisDevice());
        $scope.currentDevice.mountPoint = $scope.config.mountPoint;
        $scope.currentDevice.listenAddressesStr = $scope.config.options.listenAddress.join(', ');

        $scope.settingsEditor.$setPristine();
        $('#editSettings').modal();
    };

    $scope.saveSettings = function() {
        $('#editSettings').modal('hide');

        $scope.thisDevice().name = $scope.currentDevice.name;
        $scope.config.mountPoint = $scope.currentDevice.mountPoint;
        $scope.config.options.listenAddress = $scope.currentDevice.listenAddressesStr.split(',').map(function (x) {
            return x.trim();
        });

        $scope.saveConfig();
    }

    $scope.addFolder = function() {
        $scope.currentFolder = {
            selectedDevices: {},
            cacheSize: '512 MiB'
        };

        $scope.editingExisting = false;
        $scope.folderEditor.$setPristine();
        $('#editFolder').modal();
    };

    $scope.editFolder = function(folderCfg) {
        $scope.currentFolder = angular.copy(folderCfg);

        $scope.currentFolder.selectedDevices = {};
        folderCfg.devices.forEach(function (n) {
            $scope.currentFolder.selectedDevices[n.deviceID] = true;
        });

        $scope.editingExisting = true;
        $scope.folderEditor.$setPristine();
        $('#editFolder').modal();
    };

    $scope.saveFolder = function() {
        $('#editFolder').modal('hide');

        var folderCfg = $scope.currentFolder;
        folderCfg.devices = [];
        folderCfg.devices.push({ deviceID: $scope.config.myID });
        $scope.config.devices.forEach(function (d) {
            if (folderCfg.selectedDevices[d.deviceID]) {
                folderCfg.devices.push({ deviceID: d.deviceID });
            }
        });

        var folders = [];
        folders.push(folderCfg);
        for (var i=0 ; i<$scope.config.folders.length ; i++) {
            if ($scope.config.folders[i].id !== folderCfg.id) {
                folders.push($scope.config.folders[i])
            }
        }
        $scope.config.folders = folders;
        $scope.config.folders.sort(function(a, b) {
            return a.id.localeCompare(b.id);
        });

        $scope.saveConfig();
    };

    $scope.deleteFolder = function() {
        $('#editFolder').modal('hide');

        var folderCfg = $scope.currentFolder;

        var i = $scope.config.folders.findIndex(function(el) { return el.id === folderCfg.id });
        $scope.config.folders.splice(i, 1);

        $scope.saveConfig();
    };

    $scope.editPinnedFiles = function(folder) {
        $scope.currentFolder = angular.copy(folder);

        if (null == $scope.currentFolder.pinnedFiles) {
            $scope.currentFolder.pinnedFiles = [];
        }

        $scope.editingExisting = true;
        $('#editPins').modal();
    };

    $scope.addPin = function() {
        var toAdd = $scope.pinFilePath.trim();

        var i = $scope.currentFolder.pinnedFiles.findIndex(function(el) { return el === toAdd });
        if (-1 == i) {
            $scope.currentFolder.pinnedFiles.push($scope.pinFilePath);

            $scope.currentFolder.pinnedFiles.sort(function(a, b) {
                return a.localeCompare(b);
            });
        }
        $scope.pinFilePath = "";
    };

    $scope.removePin = function(file) {
        var i = $scope.currentFolder.pinnedFiles.findIndex(function(el) { return el === file });
        $scope.currentFolder.pinnedFiles.splice(i, 1);
    };

    $scope.savePins = function() {
        $('#editPins').modal('hide');

        var folderCfg = $scope.currentFolder;
        $scope.config.folders.forEach(function (f) {
            if (f.id === folderCfg.id) {
                f.pinnedFiles = folderCfg.pinnedFiles;
            }
        });

        $scope.saveConfig();
    };

    $scope.pinAutocomplete = [];

    $scope.updatePinsAutocomplete = function() {
        if (!$scope.currentFolder) {
            $scope.pinAutocomplete = [];
            return;
        }

        var params = {
            folderID: $scope.currentFolder.id,
            pathPrefix: $scope.pinFilePath
        };
        $http.get('/api/db/browse', {params: params}).then(
            function(result) { $scope.pinAutocomplete = result.data },
            function() {} // TODO handle errors
            );
    };

    $scope.saveConfig = function () {
        var cfg = JSON.stringify($scope.config);
        var opts = {
            headers: {
                'Content-Type': 'application/json'
            }
        };
        $http.post('/api/system/config', cfg, opts).then(
            function () {
                $scope.configInSync = false;
                window.scrollTo(0, 0);
            },
            function () {
                // TODO show error message
            });
    };

    initController();
});
