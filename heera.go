package main

import "strings"

const heeraUsername = "heera"

func isHeera(username string) bool {
	return strings.EqualFold(username, heeraUsername)
}

func blockProtected(viewer, target string) bool {
	return !isHeera(viewer) && isTopThreeScorer(target)
}

func heeraApplyStyle(rs *rankStyle) {
	rs.container = "border:3px solid transparent !important;background-image:linear-gradient(135deg,rgba(251,207,232,.3),rgba(221,214,254,.25),rgba(186,230,253,.3)),linear-gradient(var(--post-bg),var(--post-bg)),linear-gradient(120deg,#f9a8d4,#c4b5fd,#93c5fd,#86efac,#fde68a,#fdba74,#f9a8d4);background-origin:border-box;background-clip:padding-box,padding-box,border-box;background-size:100% 100%,100% 100%,300% 100%;"
	rs.avatar = "border:3px solid #f472b6 !important;box-shadow:0 0 0 2px var(--post-bg),0 0 0 4px #c4b5fd;"
	rs.frameClass = "heera-frame"
	rs.bottomBar = `<div style="height:5px;width:100%;background-image:linear-gradient(90deg,#f9a8d4,#c4b5fd,#93c5fd,#86efac,#fde68a,#fdba74,#f9a8d4);"></div><div style="text-align:center;padding:5px 0 6px;font-size:13px;letter-spacing:6px;background:linear-gradient(180deg,rgba(244,114,182,.14),transparent);">🦋🌸🦋🌷🦋🌼🦋</div>`
}

func heeraCSSFor(author string) string {
	if !isHeera(author) {
		return ""
	}
	return heeraCSS
}

const heeraCSS = `<style id="heera-styles">
@keyframes heeraBorderFlow{0%{background-position:0 0,0 0,0% 50%}100%{background-position:0 0,0 0,300% 50%}}
@keyframes heeraFlutter{0%,100%{transform:translate(0,0) rotate(-8deg) scale(1)}25%{transform:translate(-6px,-8px) rotate(10deg) scale(1.15)}50%{transform:translate(4px,-3px) rotate(-6deg) scale(.95)}75%{transform:translate(-3px,5px) rotate(8deg) scale(1.1)}}
.heera-frame{position:relative;animation:heeraBorderFlow 9s linear infinite,rankFloatIn .5s ease-out !important;box-shadow:0 8px 22px -10px rgba(244,114,182,.55);}
.heera-frame::before{content:"🦋";position:absolute;top:34px;right:10px;font-size:20px;z-index:5;pointer-events:none;animation:heeraFlutter 3.2s ease-in-out infinite;}
.heera-frame::after{content:"🦋";position:absolute;bottom:46px;left:8px;font-size:16px;z-index:5;pointer-events:none;opacity:.9;animation:heeraFlutter 4.1s ease-in-out infinite reverse;}
@media (prefers-reduced-motion:reduce){.heera-frame,.heera-frame::before,.heera-frame::after{animation:none !important}}
</style>`