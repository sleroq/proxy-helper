Name: ph
Version: %{package_version}
Release: 1
Summary: Subscription-managed proxy controller with bundled cores
License: LicenseRef-Ph AND GPL-3.0-only AND GPL-3.0-or-later
Requires: ca-certificates, systemd
AutoReqProv: no

%description
Subscription-managed proxy controller with bundled cores.

%install
mkdir -p %{buildroot}
cp -a %{payload}/. %{buildroot}/

%post
/usr/lib/ph/package-install

%preun
if [ "$1" -eq 0 ]; then /usr/lib/ph/package-remove; fi

%files
%defattr(-,root,root,-)
%dir /etc/ph
%config(noreplace) /etc/ph/config.json
%config(noreplace) /etc/ph/sing-box.json
%config(noreplace) /etc/ph/mihomo.json
/usr/bin/ph
/usr/lib/ph
/usr/lib/systemd/system/ph-proxy.service
/usr/lib/systemd/system/ph-proxy-update.service
/usr/lib/systemd/system/ph-proxy-update.timer
/usr/share/doc/ph
