#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

"""Render the illustrated web guide with embedded Cyrillic fonts and PDF bookmarks."""
import html
import os
import re
from pathlib import Path

from reportlab.lib import colors
from reportlab.lib.enums import TA_LEFT
from reportlab.lib.pagesizes import A4, landscape
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import (
    Image, Paragraph, Preformatted, SimpleDocTemplate, Spacer, PageBreak, KeepTogether,
)
from reportlab.platypus.tableofcontents import TableOfContents

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / "output/pdf/godhcp-web-guide.pdf"
VERSION = re.search(r'Version = "([^"]+)"', (ROOT / "internal/version/version.go").read_text()).group(1)
FONT_DIR = Path(os.environ.get("GODHCP_PDF_FONTS", "/usr/share/fonts/truetype/dejavu"))
for name, filename in (("Guide", "DejaVuSans.ttf"), ("GuideBold", "DejaVuSans-Bold.ttf"), ("GuideMono", "DejaVuSansMono.ttf")):
    path = FONT_DIR / filename
    if not path.exists():
        raise SystemExit(f"Missing {path}; install DejaVu fonts or set GODHCP_PDF_FONTS")
    pdfmetrics.registerFont(TTFont(name, str(path)))
pdfmetrics.registerFontFamily("Guide", normal="Guide", bold="GuideBold", italic="Guide", boldItalic="GuideBold")

PAGE_WIDTH, PAGE_HEIGHT = landscape(A4)
MARGIN = 43
CONTENT_WIDTH = PAGE_WIDTH - 2 * MARGIN
INK = colors.HexColor("#202C38")
ACCENT = colors.HexColor("#234A6C")
MUTED = colors.HexColor("#536170")
styles = getSampleStyleSheet()
body = ParagraphStyle("GuideBody", fontName="Guide", fontSize=10, leading=14, textColor=INK, spaceAfter=7)
h2 = ParagraphStyle("GuideH2", parent=body, fontName="GuideBold", fontSize=15, leading=20, textColor=ACCENT, spaceBefore=16, spaceAfter=9, keepWithNext=True)
h3 = ParagraphStyle("GuideH3", parent=h2, fontSize=11.2, leading=16, spaceBefore=11)
bullet = ParagraphStyle("GuideBullet", parent=body, leftIndent=12, firstLineIndent=-9, spaceAfter=6)
code = ParagraphStyle("GuideCode", fontName="GuideMono", fontSize=7.7, leading=10.5, leftIndent=8, rightIndent=8, backColor=colors.HexColor("#F2F4F6"), borderPadding=7, spaceBefore=5, spaceAfter=11)


def normalize(text):
    return text.replace("\u2014", "-").replace("\u2013", "-").replace("\u2011", "-")


def inline(text):
    tokens = {}

    def protect(value):
        key = f"ZZTOKEN{len(tokens)}ZZ"
        tokens[key] = value
        return key

    text = normalize(text)
    text = re.sub(r'`([^`]+)`', lambda m: protect(f'<font name="GuideMono" size="8.5">{html.escape(m[1])}</font>'), text)

    def link(match):
        label, target = match.groups()
        label = html.escape(label)
        if target.startswith(("http://", "https://")):
            return protect(f'<link href="{html.escape(target, quote=True)}" color="#234A6C">{label}</link>')
        return protect(f'{label} <font size="8" color="#536170">({html.escape(target)})</font>')

    text = re.sub(r'\[([^]]+)\]\(([^)]+)\)', link, text)
    text = html.escape(text)
    text = re.sub(r'\*\*([^*]+)\*\*', r'<b>\1</b>', text)
    for key, value in tokens.items():
        text = text.replace(key, value)
    return text


class GuideDocument(SimpleDocTemplate):
    def afterFlowable(self, flowable):
        if isinstance(flowable, Paragraph) and hasattr(flowable, "bookmark"):
            key = flowable.bookmark
            title = flowable.getPlainText()
            level = 0 if flowable.style.name == "GuideH2" else 1
            self.canv.bookmarkPage(key)
            self.canv.addOutlineEntry(title, key, level=level, closed=False)
            self.notify("TOCEntry", (level, title, self.page, key))


def furniture(canvas, doc):
    canvas.saveState()
    canvas.setFillColor(MUTED)
    canvas.setFont("Guide", 7.4)
    canvas.drawString(MARGIN, PAGE_HEIGHT - 26, f"GoDHCP {VERSION} / Веб-интерфейс: пошаговая инструкция")
    canvas.setStrokeColor(colors.HexColor("#D7DDE3"))
    canvas.line(MARGIN, 35, PAGE_WIDTH - MARGIN, 35)
    canvas.drawString(MARGIN, 23, "Редакция 30 октября 2026 / Иллюстрированное руководство")
    canvas.drawRightString(PAGE_WIDTH - MARGIN, 23, str(doc.page))
    canvas.restoreState()


story = [
    Spacer(1, 34),
    Paragraph("GoDHCP", ParagraphStyle("CoverTitle", parent=h2, fontSize=30, leading=38)),
    Paragraph("Веб-интерфейс: пошаговая инструкция", ParagraphStyle("CoverSubtitle", parent=h2, fontSize=22, leading=30)),
    Paragraph(f"Версия {VERSION} · Работа через браузер", body),
    Paragraph("© 2026 Кислов Роман Сергеевич. Apache License 2.0.", body),
    Spacer(1, 16),
    Paragraph("Для начинающего администратора: вход, поиск устройства, постоянный IP для принтера, подсети, VLAN, relay, журнал и конфигурация. Скриншоты настоящей панели на демонстрационном стенде.", body),
    Spacer(1, 10),
    Paragraph("Адреса на картинках - примеры. Пользуйтесь параметрами, согласованными для вашей сети. Скриншоты можно увеличить в просмотрщике PDF.", ParagraphStyle("CoverNote", parent=body, textColor=ACCENT)),
    Spacer(1, 18),
    Paragraph("Содержание", h2),
]
toc = TableOfContents()
toc.levelStyles = [
    ParagraphStyle("TOC0", fontName="Guide", fontSize=9.4, leading=15, textColor=INK, spaceBefore=4),
    ParagraphStyle("TOC1", fontName="Guide", fontSize=8.4, leading=12, leftIndent=14, textColor=MUTED),
]
story += [toc, PageBreak()]
source = (ROOT / "docs/web-guide.md").read_text()
# Keep the upgrade procedure identical in Markdown and both PDF guides.
upgrade = (ROOT / "docs/upgrade-v0.1.7.md").read_text().split("\n", 1)[1]
source = source.replace("<!-- include: upgrade-v0.1.7.md -->",
                        upgrade.replace("\n## ", "\n### "))
lines = source.splitlines()
i = 1  # The title is rendered on the cover.
heading_count = 0
while i < len(lines):
    line = lines[i]
    if not line.strip():
        i += 1
        continue
    image_match = re.fullmatch(r"!\[([^]]*)\]\(([^)]+)\)", line)
    if image_match:
        caption, relative = image_match.groups()
        path = ROOT / "docs" / relative
        image = Image(str(path))
        scale = min(CONTENT_WIDTH / image.imageWidth, 405 / image.imageHeight)
        image.drawWidth = image.imageWidth * scale
        image.drawHeight = image.imageHeight * scale
        story.append(KeepTogether([Spacer(1, 8), image, Spacer(1, 5), Paragraph(inline(caption), body)]))
        i += 1
        # Caption is rendered with the figure; avoid repeating Markdown caption.
        if i < len(lines) and not lines[i].strip(): i += 1
        if i < len(lines) and lines[i].strip() == caption: i += 1
        continue
    if line.startswith("```"):
        i += 1
        block = []
        while i < len(lines) and not lines[i].startswith("```"):
            block.append(normalize(lines[i]))
            i += 1
        longest = max((pdfmetrics.stringWidth(x, "GuideMono", code.fontSize) for x in block), default=1)
        block_style = ParagraphStyle(f"Code{heading_count}-{i}", parent=code)
        if longest > CONTENT_WIDTH - 30:
            block_style.fontSize = code.fontSize * (CONTENT_WIDTH - 30) / longest
            block_style.leading = max(block_style.fontSize * 1.4, 9)
        if block_style.fontSize < 6.3:
            raise SystemExit(f"Code line too long for readable PDF near Markdown line {i}")
        story.append(KeepTogether([Preformatted("\n".join(block), block_style)]))
        i += 1
        continue
    if line.startswith(("## ", "### ")):
        level3 = line.startswith("### ")
        if not level3 and heading_count > 0:
            story.append(PageBreak())
        title = line[4 if level3 else 3:]
        paragraph = Paragraph(inline(title), h3 if level3 else h2)
        paragraph.bookmark = f"section-{heading_count}"
        heading_count += 1
        story.append(paragraph)
        i += 1
        continue
    if line.startswith("- ") or re.match(r"\d+\. ", line):
        if line.startswith("- "):
            value = "• " + line[2:]
        else:
            value = line
        story.append(Paragraph(inline(value), bullet))
        i += 1
        continue
    paragraph = [line.strip()]
    i += 1
    while i < len(lines) and lines[i].strip() and not lines[i].startswith(("#", "```", "- ", "![")) and not re.match(r"\d+\. ", lines[i]):
        paragraph.append(lines[i].strip())
        i += 1
    story.append(Paragraph(inline(" ".join(paragraph)), body))

OUTPUT.parent.mkdir(parents=True, exist_ok=True)
doc = GuideDocument(str(OUTPUT), pagesize=landscape(A4), leftMargin=MARGIN, rightMargin=MARGIN,
                    topMargin=45, bottomMargin=49, title="GoDHCP - Веб-интерфейс: пошаговая инструкция",
                    author="Кислов Роман Сергеевич", pageCompression=1)
doc.multiBuild(story, onFirstPage=furniture, onLaterPages=furniture)
# Keep standalone PDF distribution accompanied by its own notices and font license.
from pypdf import PdfWriter
writer = PdfWriter(clone_from=str(OUTPUT))
for name, path in (("LICENSE.txt", ROOT / "LICENSE"), ("NOTICE.txt", ROOT / "NOTICE"),
                   ("DejaVu-LICENSE.txt", ROOT / "third_party/licenses/dejavu/LICENSE")):
    writer.add_attachment(name, path.read_bytes())
attached = OUTPUT.with_suffix(".attached.pdf")
writer.write(attached)
attached.replace(OUTPUT)
print(OUTPUT)
