# -*- coding: utf-8 -*-
"""End-to-end check of the practice module."""
import sys
from playwright.sync_api import sync_playwright

sys.stdout.reconfigure(encoding="utf-8", errors="replace")
BASE = "http://127.0.0.1:8090"

with sync_playwright() as pw:
    browser = pw.chromium.launch(channel="chrome", headless=True)
    ctx = browser.new_context(viewport={"width": 1280, "height": 900})
    page = ctx.new_page()
    errors = []
    page.on("console", lambda m: errors.append(m.text) if m.type == "error" else None)
    page.on("pageerror", lambda e: errors.append(str(e)))

    page.goto(BASE + "/practice", wait_until="networkidle")
    print("index cards:", page.locator(".scard").count())
    print("overall:", page.inner_text(".overall").replace("\n", " "))
    print("politics card:", page.locator(".scard").first.inner_text().replace("\n", " | "))

    page.click("a.scard[href='/practice/politics']")
    page.wait_for_load_state("networkidle")
    print("politics questions:", page.locator(".q").count())
    print("section stats:", page.inner_text(".overall").replace("\n", " "))

    # answer question 2 correctly (D)
    q2 = page.locator(".q").nth(1)
    q2.locator("input[value='D']").check()
    q2.locator("button.submit").click()
    page.wait_for_timeout(700)
    print("q2 verdict:", q2.locator(".verdict").inner_text().strip())
    print("q2 explanation visible:", q2.locator(".explainbox").is_visible())
    print("section stats after:", page.inner_text(".overall").replace("\n", " "))

    page.reload(wait_until="networkidle")
    q2 = page.locator(".q").nth(1)
    print("q2 persisted checked:", q2.locator("input[value='D']").is_checked(),
          "| verdict:", q2.locator(".verdict").inner_text().strip())

    # essay self-assessment
    page.goto(BASE + "/practice/essay", wait_until="networkidle")
    first = page.locator(".q").first
    first.locator("summary").click()
    first.locator("[data-choice='review']").click()
    page.wait_for_timeout(700)
    print("essay verdict:", first.locator(".verdict").inner_text().strip())

    # reset politics
    page.on("dialog", lambda d: d.accept())
    page.goto(BASE + "/practice/politics", wait_until="networkidle")
    page.click(".reset button")
    page.wait_for_load_state("networkidle")
    print("after reset stats:", page.inner_text(".overall").replace("\n", " "))
    print("after reset checked count:", page.locator("input[type=radio]:checked").count())

    # handbook CTA and header practice link
    page.goto(BASE + "/", wait_until="networkidle")
    print("cta count:", page.locator(".practice-cta a").count())
    print("header practice link:", page.locator("a.pbtn[href='/practice']").count())
    page.click("a.pbtn[href='/practice']")
    page.wait_for_load_state("networkidle")
    print("navigated to:", page.url)

    page.screenshot(path="shot_practice.png")
    m = ctx.new_page()
    m.set_viewport_size({"width": 390, "height": 844})
    m.goto(BASE + "/practice/politics", wait_until="networkidle")
    m.screenshot(path="shot_practice_mobile.png")

    print("errors:", errors if errors else "none")
    browser.close()
