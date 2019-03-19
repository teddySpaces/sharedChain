#### <font face="楷体">一、基础环境安装</font>
- #### 服务端（ubuntu）
#### <font face="楷体">1. docker</font>
```
apt-get install docker.io
docker -v
```
#### <font face="楷体">2. docker-compose</font>
```
curl https://bootstrap.pypa.io/get-pip.py -o get-pip.py
python get-pip.py 
pip install docker-compose
```
#### <font face="楷体">3. Nginx安装</font>
```
sudo apt-get install nginx
```
#### <font face="楷体">4. docker-compose 启动服务端程序</font>
```
1. docker-compose.yaml
version: '3.1'
services:
  lanproxy-client:
    image: franklin5/lanproxy-server
    container_name: lanproxy-server
    environment:
     # 配置后台管理账号，默认admin
     - LANPROXY_USERNAME=admin
     # 配置后台管理密码，默认admin
     - LANPROXY_PASSWORD=admin
    volumes:
     # 用于保存创建的配置文件，避免重启服务后配置消失
     - /usr/local/docker/lanproxy-server/config-data:/root/.lanproxy
    ports:
     - 8090:8090
     - 4900:4900
     - 4993:4993
     - 9000-9100:9000-9100
    restart: always
2. 启动
    docker-compose up -d    //docker-compose -f docker-compose.yaml up -d 
    docker-compose down
3. 登陆服务端lanproxy
    服务器ip:8090
```
- #### 客户端(centos)
#### <font face="楷体">1. docker</font>
```
1. 安装依赖包
sudo yum install -y yum-utils  device-mapper-persistent-data  lvm2
2. 设置稳定版仓库
sudo yum-config-manager   --add-repo  https://download.docker.com/linux/centos/docker-ce.repo
3. 安装
sudo yum install docker-ce
4. 启动docker
sudo systemctl start docker
5. 运行检测是否安装成功
sudo docker run hello-world
```
#### <font face="楷体">2. Nginx安装</font>
```
1. 添加Nginx到YUM源
sudo rpm -Uvh http://nginx.org/packages/centos/7/noarch/RPMS/nginx-release-centos-7-0.el7.ngx.noarch.rpm
2. 安装Nginx
sudo yum install -y nginx
3. 启动Nginx
service nginx start
```
#### <font face="楷体">3. docker-compose</font>
```
1. 安装企业版Linux 附加包
yum -y install epel-release
2. 安装pip
yum -y install python-pip
3. 更新pip
 $   pip install --upgrade pip
 //国内原加速
 $   pip install -i https://pypi.tuna.tsinghua.edu.cn/simple  --upgrade pip
4. 安装docker-compose
$   pip install docker-compose
//国内原加速
 $   pip install -i https://pypi.tuna.tsinghua.edu.cn/simple  docker-compose
5. 查看版本信息
$   docker-compose --version
```
#### <font face="楷体">4. docker-compose.yaml</font>
```
version: '3.1'
services:
  lanproxy-client:
    image: franklin5/lanproxy-client
    container_name: lanproxy-client
    environment:
     # 这里是在lanproxy后台配置的密钥
     - LANPROXY_KEY=input_your_key
     # 服务器的ip，支持域名
     - LANPROXY_HOST=input_your_host
    restart: always
```
#### <font face="楷体">5. 启动</font>
```
docker-compose -f docker-compose.yaml up -d
```

