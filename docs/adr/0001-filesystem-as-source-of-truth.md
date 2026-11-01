# Filesystem as source of truth

Documents and Assets live as plain files in a nested folder tree under `DATA_DIR`; there is no metadata database. A Document's identity is its Document Path. This keeps the Library inspectable, backed up by ordinary file tools, and editable via scp/Syncthing, at the cost of not supporting tags, versions, or indexed search without adding another store later.
