package modules

// StartWorkers starts every background sampler. All of them are cheap tickers:
// media is sampled once per second, specs every two seconds, and the VRChat log
// is tailed incrementally instead of being re-read.
func StartWorkers() {
	startAfkWorker()
	startMediaWorker()
	startLyricsWorker()
	startWeatherWorker()
	startLogWorker()
	startSpecsWorker()
	startSteamVRWorker()
	startHaptics()
	startHeartWorker()
	startDiscordWorker()
	startTwitchWorker()
	startSoundpadWorker()
	startVoicemodWorker()
	startNetworkWorker()
	startGPUWorker()
	startSensorWorker()
	startPingWorker()
	startRulesWorker()
	startTallyWorker()
	startStreamingWorkers()
	startAvatarWorker()
	startWorldBlockWorker()
	go watchVRChatProcess()
}
