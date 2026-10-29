# Copyright 2026 Roman Kislov
# SPDX-License-Identifier: Apache-2.0
Name: godhcp
%{!?godhcp_version:%define godhcp_version 0.1.6}
Version: %{godhcp_version}
Release: alt1.p10
Summary: DHCPv4 server with embedded web management
License: Apache-2.0
Group: System/Servers
Url: https://github.com/rkislov/gdhcp
Source: godhcp-payload.tar.gz
BuildArch: x86_64
AutoReqProv: no
Requires: systemd shadow-utils util-linux

%description
Copyright 2026 Кислов Роман Сергеевич.
DHCPv4 server with persistent leases, REST API and embedded Go templates web UI.
Includes the illustrated web interface guide in PDF format.

%prep
%setup -q -c

%install
mkdir -p %buildroot
cp -a ./* %buildroot/

%post
getent group godhcp >/dev/null || groupadd -r godhcp
id godhcp >/dev/null 2>&1 || useradd -r -g godhcp -d /var/lib/godhcp -s /sbin/nologin godhcp
install -d -m 0750 -o godhcp -g godhcp /etc/godhcp /var/lib/godhcp
if test -d /run/systemd/system; then systemctl daemon-reload; fi

%preun
if test "$1" = 0 && test -d /run/systemd/system; then
 systemctl stop godhcp.service || :
 systemctl disable godhcp.service || :
fi

%postun
if test -d /run/systemd/system; then systemctl daemon-reload; fi

%files
/usr/bin/godhcp
/usr/lib/systemd/system/godhcp.service
%config(noreplace) /etc/godhcp/config.yml.example
%doc /usr/share/doc/godhcp
