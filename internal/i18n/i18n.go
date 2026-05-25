// Package i18n provides a tiny message catalog with English and Traditional
// Chinese (zh-TW) translations.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

type Lang string

const (
	EN   Lang = "en"
	ZHTW Lang = "zh-TW"
)

var catalog = map[string]map[Lang]string{
	"welcome_receive": {
		EN:   "Mod-pack receiver. This will update your MCE2 install.",
		ZHTW: "模組包接收程式，將會更新你的 MCE2 安裝。",
	},
	"welcome_send": {
		EN:   "Mod-pack sender.",
		ZHTW: "模組包傳送程式。",
	},
	"locating_instance": {
		EN:   "Looking for your MCE2 instance...",
		ZHTW: "正在尋找你的 MCE2 安裝位置…",
	},
	"instance_found": {
		EN:   "Found instance: %s",
		ZHTW: "已找到安裝位置：%s",
	},
	"instance_not_found": {
		EN:   "Could not find an MCE2 instance automatically. Set instance_path in the config file.",
		ZHTW: "無法自動找到 MCE2 安裝位置。請在設定檔中填入 instance_path。",
	},
	"enter_code": {
		EN:   "Enter the code phrase from the sender, then press Enter: ",
		ZHTW: "請輸入傳送方提供的代碼，然後按 Enter：",
	},
	"receiving": {
		EN:   "Receiving update...",
		ZHTW: "正在接收更新…",
	},
	"applying": {
		EN:   "Applying update (a backup is being made first)...",
		ZHTW: "正在套用更新（會先自動備份）…",
	},
	"apply_done": {
		EN:   "Done! Updated %d files, removed %d. Backup saved at: %s",
		ZHTW: "完成！更新了 %d 個檔案，移除了 %d 個。備份位置：%s",
	},
	"version_mismatch": {
		EN:   "Warning: package is for %s but your install looks like %s. Continue anyway? [y/N]: ",
		ZHTW: "警告：此更新包適用於 %s，但你的安裝看起來是 %s。仍要繼續嗎？[y/N]：",
	},
	"cancelled": {
		EN:   "Cancelled. Nothing was changed.",
		ZHTW: "已取消，沒有任何變更。",
	},
	"sending": {
		EN:   "Building delta and sending. Give this code to the other person:",
		ZHTW: "正在建立差異並傳送。請把這個代碼給對方：",
	},
	"send_waiting": {
		EN:   "Waiting for the other side to receive...",
		ZHTW: "正在等待對方接收…",
	},
	"send_done": {
		EN:   "Transfer complete.",
		ZHTW: "傳送完成。",
	},
	"delta_summary": {
		EN:   "Delta: %d files to send, %d to remove (%s).",
		ZHTW: "差異：%d 個檔案要傳送，%d 個要移除（%s）。",
	},
	"error_logged": {
		EN:   "Something went wrong. Please send this log file to support: %s",
		ZHTW: "發生錯誤。請把這個記錄檔傳給技術支援：%s",
	},
	"press_enter_exit": {
		EN:   "Press Enter to exit.",
		ZHTW: "按 Enter 結束。",
	},
	"scanning": {
		EN:   "Scanning your mod-pack...",
		ZHTW: "正在掃描你的模組包…",
	},
	"baseline_missing": {
		EN:   "No baseline.json found. A baseline (snapshot of a fresh install) is required to send. Expected at: %s",
		ZHTW: "找不到 baseline.json。傳送前需要一份基準檔（全新安裝的快照）。預期位置：%s",
	},
	"capturing_baseline": {
		EN:   "Capturing baseline from this (fresh) install...",
		ZHTW: "正在從這個（全新）安裝建立基準檔…",
	},
	"baseline_saved": {
		EN:   "Baseline saved: %s (%d files).",
		ZHTW: "基準檔已儲存：%s（%d 個檔案）。",
	},
	"nothing_to_send": {
		EN:   "No customizations differ from the baseline. Nothing to send.",
		ZHTW: "沒有任何與基準不同的自訂內容，無需傳送。",
	},
	"saved_file": {
		EN:   "Delta package saved: %s",
		ZHTW: "差異更新包已儲存：%s",
	},
	"applying_file": {
		EN:   "Applying update from file: %s",
		ZHTW: "正在從檔案套用更新：%s",
	},
	"apply_kept": {
		EN:   "Note: kept %d base files you had changed (not removed).",
		ZHTW: "注意：保留了 %d 個你曾修改過的基礎檔案（未移除）。",
	},
	"rollback_done": {
		EN:   "Rolled back the last update from backup: %s",
		ZHTW: "已從備份還原上一次更新：%s",
	},
	"rollback_none": {
		EN:   "Nothing to roll back: %s",
		ZHTW: "沒有可還原的內容：%s",
	},
}

// T is the active translator.
type T struct{ lang Lang }

// New returns a translator for the given language, falling back to English.
func New(lang Lang) *T {
	if lang != ZHTW {
		lang = EN
	}
	return &T{lang: lang}
}

// Detect picks a language from --lang, then the LANG/LC_ALL environment,
// falling back to Traditional Chinese. (On Windows LANG is usually unset, so the
// zh-TW user gets Chinese by fallback; a Linux LANG=en_* gives English.)
func Detect(override string) Lang {
	switch strings.ToLower(override) {
	case "zh", "zh-tw", "zh_tw", "tw":
		return ZHTW
	case "en", "en-us", "en_us":
		return EN
	}
	env := strings.ToLower(os.Getenv("LC_ALL") + os.Getenv("LANG"))
	if strings.HasPrefix(env, "en") || strings.Contains(env, "en_") {
		return EN
	}
	return ZHTW
}

// S returns the translated string for key (untranslated keys return the key).
func (t *T) S(key string) string {
	if m, ok := catalog[key]; ok {
		if s, ok := m[t.lang]; ok {
			return s
		}
		if s, ok := m[EN]; ok {
			return s
		}
	}
	return key
}

// F is S with printf-style formatting.
func (t *T) F(key string, args ...any) string {
	return fmt.Sprintf(t.S(key), args...)
}
