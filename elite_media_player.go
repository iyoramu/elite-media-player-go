package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2/driver/desktop"
	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"
)

type MediaPlayer struct {
	app        fyne.App
	window     fyne.Window
	playlist   []string
	current    int
	streamer   beep.StreamSeekCloser
	format     beep.Format
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *beep.Volume
	isPlaying  bool
	timeSlider *widget.Slider
	timeLabel  *widget.Label
	volumeCtrl *widget.Slider
	playBtn    *widget.Button
	coverArt   *canvas.Image
	titleLabel *widget.Label
	artistLabel *widget.Label
	playlistUI *widget.List
	progressUpdate chan bool
}

func NewMediaPlayer() *MediaPlayer {
	player := &MediaPlayer{
		app:        app.NewWithID("worldclass.media.player"),
		playlist:   make([]string, 0),
		current:    -1,
		isPlaying:  false,
		progressUpdate: make(chan bool),
	}

	player.window = player.app.NewWindow("Elite Media Player")
	player.window.Resize(fyne.NewSize(800, 600))
	player.window.SetMaster()

	player.initUI()
	player.setupShortcuts()
	player.loadPreferences()

	return player
}

func (p *MediaPlayer) initUI() {
	// Custom theme with dark mode
	p.app.Settings().SetTheme(&ModernTheme{})

	// Cover art placeholder
	p.coverArt = canvas.NewImageFromResource(theme.FyneLogo())
	p.coverArt.FillMode = canvas.ImageFillContain

	// Track info
	p.titleLabel = widget.NewLabel("No track selected")
	p.titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	p.artistLabel = widget.NewLabel("")
	
	// Time slider
	p.timeSlider = widget.NewSlider(0, 1)
	p.timeSlider.Step = 0.01
	p.timeSlider.OnChanged = func(pos float64) {
		if p.streamer != nil {
			newPos := int(float64(p.streamer.Len()) * pos)
			if err := p.streamer.Seek(newPos); err != nil {
				log.Println("Seek error:", err)
			}
		}
	}
	p.timeLabel = widget.NewLabel("00:00 / 00:00")

	// Volume control
	p.volumeCtrl = widget.NewSlider(0, 1)
	p.volumeCtrl.Value = 0.8
	p.volumeCtrl.OnChanged = func(vol float64) {
		if p.volume != nil {
			p.volume.Volume = vol - 1 // Range -1 to 0
		}
	}

	// Control buttons
	p.playBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), p.togglePlay)
	prevBtn := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), p.prevTrack)
	nextBtn := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), p.nextTrack)
	openBtn := widget.NewButtonWithIcon("Add Files", theme.FolderOpenIcon(), p.openFiles)
	clearBtn := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), p.clearPlaylist)

	// Playlist
	p.playlistUI = widget.NewList(
		func() int { return len(p.playlist) },
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(filepath.Base(p.playlist[i]))
		},
	)
	p.playlistUI.OnSelected = func(id widget.ListItemID) {
		p.current = id
		p.playTrack()
	}

	// Layout
	controls := container.NewHBox(
		prevBtn,
		p.playBtn,
		nextBtn,
		layout.NewSpacer(),
		widget.NewIcon(theme.VolumeUpIcon()),
		p.volumeCtrl,
	)

	playerInfo := container.NewVBox(
		p.titleLabel,
		p.artistLabel,
		p.timeSlider,
		container.NewHBox(
			p.timeLabel,
			layout.NewSpacer(),
			openBtn,
			clearBtn,
		),
		controls,
	)

	mainContent := container.NewBorder(
		container.NewVBox(
			container.NewGridWithColumns(2, p.coverArt, playerInfo),
			widget.NewSeparator(),
		),
		nil,
		nil,
		nil,
		p.playlistUI,
	)

	p.window.SetContent(mainContent)
	p.window.SetCloseIntercept(func() {
		p.savePreferences()
		p.window.Close()
	})
}

func (p *MediaPlayer) setupShortcuts() {
	desk, ok := p.window.(desktop.Window)
	if !ok {
		return
	}

	desk.SetShortcut(&desktop.CustomShortcut{
		KeyName: fyne.KeySpace,
		Modifier: 0,
	}, p.togglePlay)

	desk.SetShortcut(&desktop.CustomShortcut{
		KeyName: fyne.KeyRight,
		Modifier: 0,
	}, func() {
		if p.streamer != nil {
			newPos := p.streamer.Position() + p.format.SampleRate.N(time.Second*5)
			if newPos >= p.streamer.Len() {
				newPos = p.streamer.Len() - 1
			}
			p.streamer.Seek(newPos)
		}
	})

	desk.SetShortcut(&desktop.CustomShortcut{
		KeyName: fyne.KeyLeft,
		Modifier: 0,
	}, func() {
		if p.streamer != nil {
			newPos := p.streamer.Position() - p.format.SampleRate.N(time.Second*5)
			if newPos < 0 {
				newPos = 0
			}
			p.streamer.Seek(newPos)
		}
	})

	desk.SetShortcut(&desktop.CustomShortcut{
		KeyName: fyne.KeyUp,
		Modifier: 0,
	}, func() {
		p.volumeCtrl.SetValue(p.volumeCtrl.Value + 0.1)
	})

	desk.SetShortcut(&desktop.CustomShortcut{
		KeyName: fyne.KeyDown,
		Modifier: 0,
	}, func() {
		p.volumeCtrl.SetValue(p.volumeCtrl.Value - 0.1)
	})
}

func (p *MediaPlayer) togglePlay() {
	if p.current < 0 || p.current >= len(p.playlist) {
		return
	}

	if p.isPlaying {
		p.pauseTrack()
	} else {
		p.playTrack()
	}
}

func (p *MediaPlayer) playTrack() {
	if p.current < 0 || p.current >= len(p.playlist) {
		return
	}

	file := p.playlist[p.current]
	f, err := os.Open(file)
	if err != nil {
		dialog.ShowError(err, p.window)
		return
	}

	var streamer beep.StreamSeekCloser
	var format beep.Format

	switch strings.ToLower(filepath.Ext(file)) {
	case ".mp3":
		streamer, format, err = mp3.Decode(f)
	case ".wav":
		streamer, format, err = wav.Decode(f)
	default:
		dialog.ShowError(fmt.Errorf("unsupported file format"), p.window)
		return
	}

	if err != nil {
		dialog.ShowError(err, p.window)
		return
	}

	// Clean up previous track
	if p.streamer != nil {
		p.streamer.Close()
	}

	p.streamer = streamer
	p.format = format
	p.ctrl = &beep.Ctrl{Streamer: beep.Loop(-1, p.streamer)}
	p.volume = &beep.Volume{Streamer: p.ctrl, Base: 2}
	p.resampler = beep.ResampleRatio(4, 1, p.volume)

	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
	speaker.Play(p.resampler)

	p.isPlaying = true
	p.playBtn.SetIcon(theme.MediaPauseIcon())

	// Update track info
	p.titleLabel.SetText(filepath.Base(file))
	p.artistLabel.SetText("Track " + strconv.Itoa(p.current+1))

	// Start progress updater
	go p.updateProgress()
}

func (p *MediaPlayer) pauseTrack() {
	speaker.Lock()
	p.isPlaying = false
	p.playBtn.SetIcon(theme.MediaPlayIcon())
	speaker.Unlock()
}

func (p *MediaPlayer) stopTrack() {
	speaker.Lock()
	p.isPlaying = false
	p.playBtn.SetIcon(theme.MediaPlayIcon())
	if p.streamer != nil {
		p.streamer.Close()
		p.streamer = nil
	}
	speaker.Unlock()
}

func (p *MediaPlayer) nextTrack() {
	if len(p.playlist) == 0 {
		return
	}

	p.stopTrack()
	p.current = (p.current + 1) % len(p.playlist)
	p.playlistUI.Select(p.current)
	p.playTrack()
}

func (p *MediaPlayer) prevTrack() {
	if len(p.playlist) == 0 {
		return
	}

	p.stopTrack()
	p.current--
	if p.current < 0 {
		p.current = len(p.playlist) - 1
	}
	p.playlistUI.Select(p.current)
	p.playTrack()
}

func (p *MediaPlayer) updateProgress() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if p.streamer == nil || !p.isPlaying {
				continue
			}

			speaker.Lock()
			pos := p.streamer.Position()
			length := p.streamer.Len()
			speaker.Unlock()

			current := float64(pos) / float64(length)
			p.timeSlider.SetValue(current)

			currentTime := time.Duration(float64(time.Second) * float64(pos) / float64(p.format.SampleRate)
			totalTime := time.Duration(float64(time.Second) * float64(length) / float64(p.format.SampleRate))
			p.timeLabel.SetText(fmt.Sprintf("%02d:%02d / %02d:%02d",
				int(currentTime.Minutes())%60, int(currentTime.Seconds())%60,
				int(totalTime.Minutes())%60, int(totalTime.Seconds())%60))

		case <-p.progressUpdate:
			return
		}
	}
}

func (p *MediaPlayer) openFiles() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		if reader == nil {
			return
		}
		defer reader.Close()

		file := reader.URI().Path()
		p.playlist = append(p.playlist, file)
		p.playlistUI.Refresh()

		if p.current == -1 {
			p.current = 0
			p.playlistUI.Select(0)
		}
	}, p.window)
}

func (p *MediaPlayer) clearPlaylist() {
	p.stopTrack()
	p.playlist = make([]string, 0)
	p.current = -1
	p.playlistUI.UnselectAll()
	p.playlistUI.Refresh()
	p.titleLabel.SetText("No track selected")
	p.artistLabel.SetText("")
	p.timeLabel.SetText("00:00 / 00:00")
	p.timeSlider.SetValue(0)
}

func (p *MediaPlayer) loadPreferences() {
	// Implement preference loading (volume, window size, etc.)
}

func (p *MediaPlayer) savePreferences() {
	// Implement preference saving
}

// ModernTheme provides a dark theme for the media player
type ModernTheme struct{}

func (m *ModernTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if variant == theme.VariantLight {
		return theme.DefaultTheme().Color(name, variant)
	}

	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x1d, G: 0x9b, B: 0xf2, A: 0xff}
	case theme.ColorNameHover:
		return color.NRGBA{R: 0x2a, G: 0x2a, B: 0x2a, A: 0xff}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 0x1d, G: 0x9b, B: 0xf2, A: 0x7f}
	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (m *ModernTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m *ModernTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (m *ModernTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameScrollBarSmall:
		return 5
	default:
		return theme.DefaultTheme().Size(name)
	}
}

func main() {
	player := NewMediaPlayer()
	player.window.ShowAndRun()
}
