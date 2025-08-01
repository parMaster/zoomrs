# bash script to backup the directory, put it in a folder with the date and time to ~/backups/
# database is a folder at /data/_db

# Get the date and time
now=$(date +"%Y-%m-%d_%H-%M-%S")

echo "Backing up database to $HOME/backups/$now.tar.gz"

# Create a folder with the date and time
mkdir -p $HOME/backups/$now

# Copy the database to the folder
cp -r /data/_db $HOME/backups/$now

cd $HOME/backups/ && tar -czf "${now}.tar.gz" "${now}" && rm -rf "${now}"

# cron job to run this script every day at 10am
# 0 10 * * * sh $HOME/go/src/zoomrs/backup_db.sh
