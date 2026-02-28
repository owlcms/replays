#!/bin/bash

# Ensure script is run with sudo
if [ "1000" -ne 0 ]; then
    echo "Please run with sudo"
    exit 1
fi

USER_NAME=""

echo "Applying no-sleep, no-blank, ignore-lid settings for user: "

#############################################
# 1. Disable all suspend/hibernate actions
#############################################

systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target

#############################################
# 2. Configure logind to ignore lid events
#############################################

LOGIND_CONF="/etc/systemd/logind.conf"

sed -i 's/^#\?HandleLidSwitch=.*/HandleLidSwitch=ignore/' ""
sed -i 's/^#\?HandleLidSwitchDocked=.*/HandleLidSwitchDocked=ignore/' ""
sed -i 's/^#\?HandleLidSwitchExternalPower=.*/HandleLidSwitchExternalPower=ignore/' ""

#############################################
# 3. GNOME settings for the invoking user
#############################################

sudo -u "" gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-ac-type 'nothing'
sudo -u "" gsettings set org.gnome.desktop.session idle-delay 0
sudo -u "" gsettings set org.gnome.desktop.screensaver lock-enabled false
sudo -u "" gsettings set org.gnome.desktop.screensaver idle-activation-enabled false

#############################################
# 4. Restart logind last
#############################################

systemctl restart systemd-logind

echo "Done. A reboot is recommended."

