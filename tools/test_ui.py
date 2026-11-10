# -*- coding: utf-8 -*-
"""End-to-end check of the server-rendered study app."""
import sys
from playwright.sync_api import sync_playwright

sys.stdout.reconfigure(encoding="utf-8", errors="replace")
URL = "http://127.0.0.1:18080/"

with sync_playwright() as pw:
    browser = pw.chromium.launch(channel="chrome", headless=True)
    ctx = browser.new_context(viewport={"width": 1440, "height": 950})
    page = ctx.new_page()
    errors = []
    page.on("console", lambda m: errors.append("console:" + m.text) if m.type == "error" else None)
    page.on("pageerror", lambda e: errors.append("pageerror:" + str(e)))

    page.goto(URL, wait_until="networkidle")
    page.wait_for_timeout(300)

    print("title:", page.title())
    print("counts:", page.inner_text("#done"), "/", page.inner_text("#total"))
    print("days:", page.inner_text("#days"), "| date:", page.input_value("#examDate"))
    print("checkboxes:", page.locator(".kp input[type=checkbox]").count())

    # toggle a visible checkpoint
    page.click("#ov2")
    page.wait_for_timeout(500)
    print("after toggle: checked=", page.is_checked("#ov2"),
          "counts=", page.inner_text("#done"), "/", page.inner_text("#total"),
          "save=", page.inner_text("#saveState"))
    page.reload(wait_until="networkidle")
    print("after reload: checked=", page.is_checked("#ov2"), "counts=", page.inner_text("#done"))

    # exam date persists
    page.evaluate("""() => {
        const d = document.getElementById('examDate');
        d.value = '2026-11-25';
        d.dispatchEvent(new Event('change', {bubbles: true}));
    }""")
    page.wait_for_timeout(600)
    print("date save:", page.inner_text("#saveState"), "| days:", page.inner_text("#days"))
    page.reload(wait_until="networkidle")
    print("date after reload:", page.input_value("#examDate"), "| days:", page.inner_text("#days"))

    # search
    page.fill("#search", "资料分析")
    page.wait_for_timeout(200)
    visible = page.locator("main .card:not(.hide)").count()
    print("search cards visible:", visible)
    page.fill("#search", "")
    page.wait_for_timeout(200)

    # only-unmastered filter hides completed items
    page.click("#filterBtn")
    page.wait_for_timeout(200)
    done_hidden = page.evaluate("""() => [...document.querySelectorAll('.kp li.done')]
        .every(li => li.classList.contains('hide'))""")
    print("filter hides done:", done_hidden)
    page.click("#filterBtn")

    # mobile nav exists and anchors work
    m = ctx.new_page()
    m.set_viewport_size({"width": 390, "height": 844})
    m.goto(URL, wait_until="networkidle")
    print("mobile nav links:", m.locator("#mnav a").count())
    m.click("#mnav a[href='#data']")
    m.wait_for_timeout(900)
    print("mobile scrolled to data:", m.evaluate("() => location.hash"))

    page.screenshot(path="shot_app.png", full_page=False)
    m.screenshot(path="shot_app_mobile.png", full_page=False)

    print("errors:", errors if errors else "none")
    browser.close()
