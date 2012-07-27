goanysync
=========

goanysync is a relatively small program to replace given directories in HDD/SSD
with symlinks to tmpfs and to sync this tmpfs contents back to HDD/SSD. It is a
rewrite of "anything-sync-daemon" with go programming language (see:
https://wiki.archlinux.org/index.php/Anything-sync-daemon).

Two main use cases are reducing wear on SSD and speeding up programs by moving
their data directories to tmpfs.


Motivation
----------

goanysync began as fork of anything-sync-daemon (by graysky), but is now
basically a complete rewrite and only the documentation and functionality still
bares resemblance to asd. Rewrote was mainly inspired by permission problems
with symlinked dirs and by the original programs bash code which, for example,
contained this line: [[ -d "$VOLATILE$i" ]] || mkdir -p "$VOLATILE$i" ||
"install -Dm755 $VOLATILE$i"


Run dependencies
----------------

* rsync


Build dependencies
------------------

* autoconf
* automake
* libtool
* go (golang)
* gzip
* txt2man


Build and install
-----------------

    ./autogen.sh
    make
    make install

Alternatively for Arch Linux an aur package is provided:
[https://aur.archlinux.org/packages.php?ID=60715](https://aur.archlinux.org/packages.php?ID=60715)


Usage
-----

Just edit installed (default location) /etc/goanysync.conf to suit your needs
and call:

    goanysync start

And remember to call:

    goanysync stop

Before booting.

Daemon scripts to do above automatically are provided for Archlinux rc.d,
systemd and upstart systems.